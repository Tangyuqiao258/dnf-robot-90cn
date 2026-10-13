package scheduler

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"robot/internal/capability/catalog"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robottemplate "robot/internal/capability/robottemplate"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/filewatch"
	"robot/internal/foundation/layout"
	"robot/internal/foundation/process"
	"robot/internal/shared"
)

func (m *RobotManager) RuntimeFileEntries() []filewatch.Entry {
	if m == nil || m.cfg == nil {
		return nil
	}
	paths := layout.New(m.cfg.ConfigDir)
	m.runtimeFilesWatched.Store(true)
	entries := []filewatch.Entry{
		{Name: "robot_config", Path: paths.RobotConfig(), Apply: m.reloadRobotConfigFile},
		{Name: "name_templates", Path: paths.NameTemplates(), Apply: m.reloadNameTemplates},
		{Name: "shout_templates", Path: paths.ShoutTemplates(), Apply: m.reloadShoutTemplates},
	}
	// Store titles belong to the private item stall; disjoint-only adapters do
	// not ship or watch that file.
	if m.supportsBackendCapability(shared.CapabilityStore) && m.itemStoreSupported() {
		entries = append(entries, filewatch.Entry{Name: "store_titles", Path: paths.StoreTitles(), Apply: m.reloadStoreTitles})
	}
	if m.supportsBackendCapability(shared.CapabilitySkill) {
		entries = append(entries, filewatch.Entry{Name: "party_skills", Path: paths.PartySkills(), Apply: func(string) error {
			_, err := m.ReloadPartySkills()
			return err
		}})
	}
	return entries
}

// StartRuntimeFileWatcher polls the manager's runtime files and applies
// validated changes without a restart. It is idempotent and is started by
// StartAutoActions; Shutdown closes it.
func (m *RobotManager) StartRuntimeFileWatcher() {
	if m == nil {
		return
	}
	m.runtimeFileWatchMu.Lock()
	defer m.runtimeFileWatchMu.Unlock()
	if m.runtimeFilePoller != nil {
		return
	}
	entries := m.RuntimeFileEntries()
	if len(entries) == 0 {
		return
	}
	poller := filewatch.New(filewatch.DefaultInterval, entries, func(entry filewatch.Entry, err error) {
		robotLogf("RUNTIME_FILE_APPLY_FAILED name=%s path=%s err=%v\n", entry.Name, entry.Path, err)
	})
	poller.Start()
	m.runtimeFilePoller = poller
}

func (m *RobotManager) stopRuntimeFileWatcher() {
	if m == nil {
		return
	}
	m.runtimeFileWatchMu.Lock()
	poller := m.runtimeFilePoller
	m.runtimeFilePoller = nil
	m.runtimeFileWatchMu.Unlock()
	if poller != nil {
		poller.Close()
	}
}

func (m *RobotManager) RobotConfig() (robotcap.ConfigResult, error) {
	path := layout.New(m.cfg.ConfigDir).RobotConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return robotcap.ConfigResult{}, err
	}
	return robotcap.ConfigResult{Path: path, Text: robotconfig.PublicText(string(data)), Config: robotconfig.Clone(m.loadRobotConfig())}, nil
}

func (m *RobotManager) UpdateRobotConfig(req robotcap.ConfigUpdateRequest) (robotcap.ConfigResult, error) {
	path := layout.New(m.cfg.ConfigDir).RobotConfig()
	if strings.TrimSpace(req.Text) != "" {
		if err := m.writeRobotConfigText(path, req.Text); err != nil {
			return robotcap.ConfigResult{}, err
		}
	} else if len(req.Updates) > 0 {
		values := make(map[string]string, len(req.Updates))
		for key, value := range req.Updates {
			values[key] = fmt.Sprint(value)
		}
		if err := m.writeRobotConfigValues(values); err != nil {
			return robotcap.ConfigResult{}, err
		}
	}
	return m.RobotConfig()
}

func (m *RobotManager) ReloadPartySkills() (catalog.PartySkillCatalogReport, error) {
	if err := m.requireBackendCapability(shared.CapabilitySkill); err != nil {
		return catalog.PartySkillCatalogReport{}, err
	}
	if m == nil || m.cfg == nil {
		return catalog.PartySkillCatalogReport{}, fmt.Errorf("missing config")
	}
	path := layout.New(m.cfg.ConfigDir).PartySkills()
	report, err := catalog.ReadPartySkillCatalog(path)
	if err != nil {
		return catalog.PartySkillCatalogReport{}, err
	}
	if len(report.Issues) > 0 {
		return report, &catalog.PartySkillCatalogValidationError{Issues: report.Issues}
	}
	if report.Enabled {
		shared.SetPartySkillStates(report.Entries)
	} else {
		shared.SetPartySkillStates(nil)
	}
	robotLogf("[RuntimeFile] applied party_skills path=%s enabled=%t entries=%d\n", path, report.Enabled, len(report.Entries))
	return report, nil
}

func (m *RobotManager) writeRobotConfigValues(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	m.configApplyMu.Lock()
	defer m.configApplyMu.Unlock()

	path := layout.New(m.cfg.ConfigDir).RobotConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := robotconfig.UpdateINIText(string(data), values)
	return m.writeRobotConfigTextLocked(path, text)
}

func (m *RobotManager) writeRobotConfigText(path, text string) error {
	m.configApplyMu.Lock()
	defer m.configApplyMu.Unlock()
	return m.writeRobotConfigTextLocked(path, text)
}

// writeRobotConfigTextLocked validates every fallible runtime requirement
// before publishing the new file. Once the atomic rename succeeds, applying
// the prepared snapshot is in-memory only and cannot leave disk and memory on
// different configurations.
func (m *RobotManager) writeRobotConfigTextLocked(path, text string) error {
	rc, err := robotconfig.Parse(text)
	if err != nil {
		return err
	}
	m.applyBackendConfigPolicy(&rc)
	if err := m.validateBackendConfig(rc); err != nil {
		return err
	}
	base, previous, err := m.prepareRobotConfigLocked(rc)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(path, []byte(text), 0644); err != nil {
		return err
	}
	m.publishRobotConfigLocked(path, base, previous)
	return nil
}

func fileModTime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

const robotConfigCheckInterval = time.Second

type robotConfigSnapshot struct {
	base      robotconfig.RuntimeConfig
	effective robotconfig.RuntimeConfig
	modTime   time.Time
	checkedAt time.Time
}

// loadRobotConfig returns an immutable configuration view. Callers may modify
// scalar fields on the returned value, but must not mutate its slice fields.
func (m *RobotManager) loadRobotConfig() robotconfig.RuntimeConfig {
	if m.runtimeFilesWatched.Load() {
		if snapshot := m.configSnapshot.Load(); snapshot != nil {
			return snapshot.effective
		}
	}
	now := time.Now()
	if snapshot := m.configSnapshot.Load(); robotConfigSnapshotFresh(snapshot, now) {
		return snapshot.effective
	}
	return m.refreshRobotConfig(now)
}

func robotConfigSnapshotFresh(snapshot *robotConfigSnapshot, now time.Time) bool {
	return snapshot != nil && !snapshot.checkedAt.IsZero() && now.Sub(snapshot.checkedAt) < robotConfigCheckInterval
}

func (m *RobotManager) refreshRobotConfig(now time.Time) robotconfig.RuntimeConfig {
	var out robotconfig.RuntimeConfig
	m.configApplyMu.Lock()
	defer m.configApplyMu.Unlock()
	m.withCache("refresh_robot_config", func() {
		if snapshot := m.configSnapshot.Load(); robotConfigSnapshotFresh(snapshot, now) {
			out = snapshot.effective
			return
		}

		configPath := layout.New(m.cfg.ConfigDir).RobotConfig()
		configMod := fileModTime(configPath)
		if snapshot := m.configSnapshot.Load(); snapshot != nil && snapshot.modTime.Equal(configMod) {
			refreshed := &robotConfigSnapshot{
				base: snapshot.base, effective: snapshot.effective,
				modTime: snapshot.modTime, checkedAt: now,
			}
			m.configSnapshot.Store(refreshed)
			out = refreshed.effective
			return
		}

		rc, err := robotconfig.LoadFile(configPath)
		if err != nil {
			if snapshot := m.configSnapshot.Load(); snapshot != nil {
				refreshed := &robotConfigSnapshot{
					base: snapshot.base, effective: snapshot.effective,
					modTime: configMod, checkedAt: now,
				}
				m.configSnapshot.Store(refreshed)
				out = refreshed.effective
				return
			}
			rc = robotconfig.Default()
		}
		robotconfig.Normalize(&rc)
		m.applyBackendConfigPolicy(&rc)
		base := robotconfig.Clone(rc)
		effective := base
		applyAdaptiveSchedulerConfig(&effective, m.adaptiveSchedulerSignals())
		snapshot := &robotConfigSnapshot{base: base, effective: effective, modTime: configMod, checkedAt: now}
		m.configSnapshot.Store(snapshot)
		out = snapshot.effective
	})
	return out
}

func (m *RobotManager) reloadRobotConfigFile(path string) error {
	m.configApplyMu.Lock()
	defer m.configApplyMu.Unlock()

	rc, err := robotconfig.LoadFile(path)
	if err != nil {
		return err
	}
	base, previous, err := m.prepareRobotConfigLocked(rc)
	if err != nil {
		return err
	}
	m.publishRobotConfigLocked(path, base, previous)
	return nil
}

func (m *RobotManager) prepareRobotConfigLocked(rc robotconfig.RuntimeConfig) (robotconfig.RuntimeConfig, *robotConfigSnapshot, error) {
	robotconfig.Normalize(&rc)
	m.applyBackendConfigPolicy(&rc)
	if err := m.validateBackendConfig(rc); err != nil {
		return robotconfig.RuntimeConfig{}, m.configSnapshot.Load(), err
	}
	base := robotconfig.Clone(rc)
	previous := m.configSnapshot.Load()
	if previous != nil && reflect.DeepEqual(previous.base, base) {
		return base, previous, nil
	}
	if previous == nil || previous.base.MaxOnlineRobots != base.MaxOnlineRobots {
		if err := process.EnsureOpenFileLimit(base.MaxOnlineRobots, 64); err != nil {
			return robotconfig.RuntimeConfig{}, previous, fmt.Errorf("apply max_online_robots=%d: %w", base.MaxOnlineRobots, err)
		}
	}
	return base, previous, nil
}

func (m *RobotManager) validateBackendConfig(rc robotconfig.RuntimeConfig) error {
	if m == nil || m.backendInfo.ID == "" {
		return nil
	}
	if limit := m.backendInfo.MaxOnline; limit > 0 && rc.AutoTargetOnlineCount > limit {
		return fmt.Errorf("auto_target_online_count=%d exceeds backend %s limit %d", rc.AutoTargetOnlineCount, m.backendInfo.ID, limit)
	}
	return nil
}

func (m *RobotManager) applyBackendConfigPolicy(rc *robotconfig.RuntimeConfig) {
	if m == nil || rc == nil || m.backendInfo.ID == "" {
		return
	}
	if limit := m.backendInfo.MaxOnline; limit > 0 {
		if rc.MaxOnlineRobots > limit {
			rc.MaxOnlineRobots = limit
		}
		if rc.MaxOnlinePerCommand > limit {
			rc.MaxOnlinePerCommand = limit
		}
		if rc.AutoTargetOnlineCount > limit {
			rc.AutoTargetOnlineCount = limit
		}
	}
	if !m.backendInfo.Supports(shared.CapabilityMailNotification) {
		rc.AutoMailNotify = false
		rc.MailEnabled = false
	}
	if !m.backendInfo.Supports(shared.CapabilityParty) {
		rc.AutoPartyAccept = false
		rc.AutoPartyFollow = false
	}
	if !m.backendInfo.Supports(shared.CapabilitySystemAnnouncement) {
		rc.AutoSystemAnnouncement = false
	}
	if !m.backendInfo.Supports(shared.CapabilityEmote) {
		rc.AutoEmote = false
	}
	if !m.backendInfo.Supports(shared.CapabilityDungeonRevive) {
		rc.AutoDungeonRevive = false
	}
	// The reply rides on the same directed-message transport as a shout, so a
	// backend that cannot send one cannot answer one either.
	if !m.backendInfo.Supports(shared.CapabilityShout) {
		rc.AutoChatReply = false
	}
	if !m.backendInfo.Supports(shared.CapabilityTrade) {
		rc.AutoTrade = false
	}
	if !m.backendInfo.Supports(shared.CapabilityServerNotice) {
		rc.AutoServerNotice = false
	}
}

func (m *RobotManager) publishRobotConfigLocked(path string, base robotconfig.RuntimeConfig, previous *robotConfigSnapshot) {
	now := time.Now()
	if previous != nil && reflect.DeepEqual(previous.base, base) {
		m.withCache("refresh_robot_config_metadata", func() {
			m.configSnapshot.Store(&robotConfigSnapshot{
				base: previous.base, effective: previous.effective,
				modTime: fileModTime(path), checkedAt: now,
			})
		})
		return
	}
	effective := robotconfig.Clone(base)
	applyAdaptiveSchedulerConfig(&effective, m.adaptiveSchedulerSignals())
	m.withCache("apply_robot_config", func() {
		m.configSnapshot.Store(&robotConfigSnapshot{
			base: base, effective: effective, modTime: fileModTime(path), checkedAt: now,
		})
	})
	if m.partyAccountRangeSink != nil {
		m.partyAccountRangeSink(base.RobotUIDStart, base.RobotUIDEnd)
	}
	if previous == nil || storePoolConfigChanged(previous.base, base) {
		m.storePoolLock.Lock()
		m.storeItemPool = nil
		m.storePoolLock.Unlock()
	}
	if previous != nil && previous.base.AutoActions && !base.AutoActions {
		m.autoMu.Lock()
		supervisor := m.supervisor
		m.autoMu.Unlock()
		m.stopAutoActorsForDisabledConfig(supervisor, base)
	}
	robotLogf("[RuntimeFile] applied robot_config path=%s\n", path)
}

func storePoolConfigChanged(old, current robotconfig.RuntimeConfig) bool {
	return old.StoreEquipmentIntensifyMin != current.StoreEquipmentIntensifyMin ||
		old.StoreEquipmentIntensifyMax != current.StoreEquipmentIntensifyMax
}

func (m *RobotManager) refreshAdaptiveRobotConfig(signals adaptiveSchedulerSignals) (robotconfig.RuntimeConfig, schedulerPolicyDecision) {
	_ = m.loadRobotConfig()
	var out robotconfig.RuntimeConfig
	var decision schedulerPolicyDecision
	m.withCache("refresh_adaptive_robot_config", func() {
		snapshot := m.configSnapshot.Load()
		if snapshot == nil {
			return
		}
		effective := snapshot.base
		decision = applyAdaptiveSchedulerConfig(&effective, signals)
		updated := &robotConfigSnapshot{
			base: snapshot.base, effective: effective,
			modTime: snapshot.modTime, checkedAt: snapshot.checkedAt,
		}
		m.configSnapshot.Store(updated)
		out = effective
	})
	return out, decision
}

func (m *RobotManager) loadShoutTemplates() robottemplate.ShoutTemplates {
	if m.cfg == nil {
		return catalog.ShoutTemplates("")
	}
	if snapshot := m.shoutTemplateSnapshot.Load(); snapshot != nil {
		return robottemplate.CloneShoutTemplates(*snapshot)
	}
	return catalog.ShoutTemplates(layout.New(m.cfg.ConfigDir).Templates)
}

func (m *RobotManager) loadNameTemplates() robottemplate.NameTemplates {
	if m.cfg == nil {
		return catalog.NameTemplates("")
	}
	if snapshot := m.nameTemplateSnapshot.Load(); snapshot != nil {
		return robottemplate.CloneNameTemplates(*snapshot)
	}
	return catalog.NameTemplates(layout.New(m.cfg.ConfigDir).Templates)
}

func (m *RobotManager) reloadShoutTemplates(path string) error {
	tpl, err := catalog.ReadShoutTemplates(path)
	if err != nil {
		return err
	}
	m.shoutTemplateSnapshot.Store(&tpl)
	robotLogf("[RuntimeFile] applied shout_templates path=%s messages=%d\n", path, len(tpl.Messages))
	return nil
}

func (m *RobotManager) reloadNameTemplates(path string) error {
	tpl, err := catalog.ReadNameTemplates(path)
	if err != nil {
		return err
	}
	m.nameTemplateSnapshot.Store(&tpl)
	robotLogf("[RuntimeFile] applied name_templates path=%s names=%d jobs=%d\n", path, robottemplate.NameCount(tpl), len(tpl.Jobs))
	return nil
}

func (m *RobotManager) reloadStoreTitles(path string) error {
	titles, err := storecap.LoadTitleCatalog(path)
	if err != nil {
		return err
	}
	m.storeTitleLock.Lock()
	m.storeTitles = titles
	m.storeTitlePath = path
	m.storeTitlesLoaded = true
	m.storeTitleSnapshot.Store(titles)
	m.storeTitlePathSnapshot.Store(&storeTitlePathValue{path: path})
	m.storeTitleLock.Unlock()
	robotLogf("[RuntimeFile] applied store_titles path=%s titles=%d\n", path, titles.Len())
	return nil
}
