package webadmin

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedAssetsContainCoreCN90UI(t *testing.T) {
	checks := []struct {
		name, content string
		required      []string
	}{
		{"index", indexHTML, []string{"openAutoDialog", "openBackendDialog", "runAction('robotsMove')", "runAction('robotsShout')", "runAction('robotsStoreAsync')", `id="mStoreCard"`}},
		{"javascript", appJS, []string{"robotsOnlineAsync", "robotsMove", "robotsShout", "robotsStoreAsync", "robotsLogoutAsync", "cleanupRobotsAsync", "dangerousDeleteAsync", "backendCapabilities", "stopRobot"}},
		{"i18n", i18nJS, []string{"I18N_MESSAGES", "toggleLanguage", "auto.target_online", "auto.shout_interval", "backend.recovery"}},
	}
	for _, check := range checks {
		for _, want := range check.required {
			if !strings.Contains(check.content, want) {
				t.Errorf("%s is missing %q", check.name, want)
			}
		}
	}
}

func TestRemovedAndUnimplementedWebSurfacesAreAbsent(t *testing.T) {
	content := indexHTML + appJS + i18nJS + appCSS
	for _, token := range []string{"openCompatDialog", "openKeyDialog", "openDiagnosticsDialog", "openMaxDialog", "openScriptDialog", "openPortsDialog", "portsButton", "game-endpoint", "submitMarketSettings", "market.", "dialog.market", "party-debug", "service-ports", "monitorPort", "auctionPort", "pointPort", "relayPort", "70 Compat", "RSA Key"} {
		if strings.Contains(content, token) {
			t.Errorf("obsolete web surface remains: %q", token)
		}
	}
}

func TestAutoDialogUsesBackendCapacityAndCapabilities(t *testing.T) {
	for _, want := range []string{"backendMaxOnline||10000", "backendCapabilities.mail_notification", "backendCapabilities.system_announcement", "backendCapabilities.dungeon_follow", "auto.auto_target_online_count", "auto.auto_shout_interval_min_sec", "auto.auto_shout_interval_max_sec", "auto.auto_system_announcement"} {
		if !strings.Contains(appJS, want) {
			t.Errorf("auto boundary is missing %q", want)
		}
	}
}

// TestNoticeCardReportsUnsupportedBackend pins the notice card boundary: a
// backend without a notice broadcast must render a localized unsupported
// marker with the capability reason instead of a misleading zero.
func TestNoticeCardReportsUnsupportedBackend(t *testing.T) {
	for _, want := range []string{"backendCapabilities.server_notice", "i18nFormat('common.unsupported')", "noticeCard.className='card'"} {
		if !strings.Contains(appJS, want) {
			t.Errorf("notice card boundary is missing %q", want)
		}
	}
	for _, want := range []string{"'common.unsupported':'Unsupported'", "'common.unsupported':'不支持'"} {
		if !strings.Contains(i18nJS, want) {
			t.Errorf("i18n table is missing %q", want)
		}
	}
}

func TestDatabaseCardUsesDashboardProjection(t *testing.T) {
	for _, want := range []string{"api('dashboardStatus')", "backendCapabilities.database", "String(r.engine).toUpperCase()", "r.writable?' · '+i18nFormat('database.writable'):''"} {
		if !strings.Contains(appJS, want) {
			t.Errorf("database projection is missing %q", want)
		}
	}
}

func TestIndexTemplateInlinesEmbeddedAssets(t *testing.T) {
	var rendered bytes.Buffer
	if err := cleanIndexTemplate.Execute(&rendered, nil); err != nil {
		t.Fatal(err)
	}
	page := rendered.String()
	for _, placeholder := range []string{appCSSPlaceholder, i18nJSPlaceholder, appJSPlaceholder} {
		if strings.Contains(page, placeholder) {
			t.Errorf("rendered index contains %q", placeholder)
		}
	}
	// A comment added to one of these assets is the usual cause of a failure
	// here: html/template parses the page after the assets are inlined and
	// drops comments from the script and style contexts, so the rendered page
	// stops containing the asset verbatim.
	for _, asset := range []struct{ name, content string }{
		{"assets/app.css", trimAssetTerminator(appCSS)},
		{"assets/i18n.js", trimAssetTerminator(i18nJS)},
		{"assets/app.js", trimAssetTerminator(appJS)},
	} {
		if !strings.Contains(page, asset.content) {
			t.Fatalf("rendered index is missing embedded %s (does it carry a comment?)", asset.name)
		}
	}
}

func TestLoginTemplateEscapesErrorAndSupportsRecovery(t *testing.T) {
	const loginError = `<script>alert("bad")</script>`
	var rendered bytes.Buffer
	if err := cleanLoginTemplate.Execute(&rendered, map[string]any{"Error": loginError, "Recovery": true}); err != nil {
		t.Fatal(err)
	}
	page := rendered.String()
	if strings.Contains(page, loginError) || !strings.Contains(page, "&lt;script&gt;") || !strings.Contains(page, `data-i18n="login.recovery"`) {
		t.Fatal("login template does not escape errors or expose recovery")
	}
}

func TestI18nLocalesHaveMatchingKeys(t *testing.T) {
	parts := strings.SplitN(i18nJS, "\n},\nzh:{\n", 2)
	if len(parts) != 2 {
		t.Fatal("cannot split locales")
	}
	re := regexp.MustCompile(`'([a-zA-Z0-9_.]+)':`)
	keys := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, x := range re.FindAllStringSubmatch(s, -1) {
			m[x[1]] = true
		}
		return m
	}
	zhTable := strings.SplitN(parts[1], "\n}};", 2)[0]
	en, zh := keys(parts[0]), keys(zhTable)
	for key := range en {
		if !zh[key] {
			t.Errorf("Chinese locale is missing %q", key)
		}
	}
	for key := range zh {
		if !en[key] {
			t.Errorf("English locale is missing %q", key)
		}
	}
}

func TestSchedulerAndActorStoreStatusAreLocalized(t *testing.T) {
	for _, want := range []string{`data-i18n="scheduler.policy_mode"`, "i18nFormat('scheduler.attach_value'", `data-i18n="robots.store"`, "i18nFormat('status.'+store)", `data-i18n="scheduler.store_policy"`, "i18nFormat('scheduler.store_value'", `data-i18n="card.store"`, `data-i18n="action.store"`} {
		if !strings.Contains(indexHTML+appJS+i18nJS, want) {
			t.Errorf("localized dashboard is missing %q", want)
		}
	}
	// The scheduler strip keeps full labels but uses symbol-separated values so
	// each metric stays on one line, with the long form in a tooltip.
	for _, want := range []string{
		`'scheduler.policy_mode':'策略模式'`,
		`'scheduler.store_policy':'摆摊策略'`,
		`'scheduler.store_policy':'Store policy'`,
		`'scheduler.store_value':'{running}/{target}·{concurrent}·{probability}%'`,
		`'scheduler.attach_value':'{rate}/s·{batch}'`,
		`'scheduler.scale_value':'+{up}/-{down}'`,
		`'scheduler.pressure_value':'{login}·{cpu}%'`,
		`'scheduler.release_value':'{breaker}·{port}'`,
		`title="Store policy: running/target · concurrency · probability"`,
		".scheditem .k,.scheditem .v{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}",
	} {
		if !strings.Contains(indexHTML+appCSS+i18nJS, want) {
			t.Errorf("compact scheduler strip is missing %q", want)
		}
	}
	for _, stale := range []string{"i18nEnglishFormat", "data-i18n-skip"} {
		if strings.Contains(indexHTML+appJS, stale) {
			t.Errorf("dashboard still forces English via %q", stale)
		}
	}
}

func TestDashboardRenderingOnlyReferencesPresentElements(t *testing.T) {
	for _, want := range []string{`id="mRobot"`, `id="mDB"`, `class="scheduler"`, `id="sMode"`, `id="sReason"`, `id="mStoreCard"`, `id="mStore"`, `id="sStore"`} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("dashboard element is missing %q", want)
		}
	}
	for _, want := range []string{"byId('mStore')", "byId('sStore')", "byId('mStoreCard')"} {
		if !strings.Contains(appJS, want) {
			t.Errorf("dashboard rendering is missing %q", want)
		}
	}
}

func TestRobotListLoadsTheWholeFleet(t *testing.T) {
	for _, want := range []string{
		"Math.max(1000,Math.min(Number(backendMaxOnline||0)||10000,20000))",
		"api('robotsStatus',{count:limit})",
	} {
		if !strings.Contains(appJS, want) {
			t.Errorf("robot list does not load the whole fleet: missing %q", want)
		}
	}
}

// A capability list where every entry is enabled leaves the missing switch
// undefined. The disabled branch must not read missing.reason then: the Auto
// dialog failed with "Cannot read properties of undefined (reading 'reason')"
// whenever all capabilities were already enabled, as in the packaged release.
func TestAutoDialogHandlesFullyEnabledCapabilities(t *testing.T) {
	if !strings.Contains(appJS, `missing?' disabled title="'+escapeHTML(missing.reason||i18nFormat('common.unavailable'))+'"':''`) {
		t.Error("auto dialog must guard the missing switch before reading its reason")
	}
	if strings.Contains(appJS, `missing?'':' disabled title="'`) {
		t.Error("auto dialog reads missing.reason when no capability is missing")
	}
}

func TestBackendRecoveryAndExplicitApplyFlowRemainAvailable(t *testing.T) {
	for _, want := range []string{"recovery_mode", "submitBackendSettings", "backendSettings", "backendCapabilities"} {
		if !strings.Contains(appJS, want) {
			t.Errorf("backend recovery flow is missing %q", want)
		}
	}
}
