package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
)

type purgeState interface {
	robotstate.Directory
	robotstate.RobotRemover
}

// SQLiteRobotPurger is the explicit maintenance path for stale 90CN robot
// data that is not visible through the runtime state table. Normal cleanup
// continues to use the game protocol.
type SQLiteRobotPurger struct {
	DatabasePath  string
	AccountPrefix string
	State         purgeState
	Sessions      sessionCloser
}

type purgeAccount struct {
	name       string
	uid        int
	totalChars int
	cids       []int
}

func (p SQLiteRobotPurger) PlanDangerousDelete(ctx context.Context, request robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error) {
	if err := p.validate(request); err != nil {
		return robotcap.DangerousDeletePlan{}, err
	}
	db, err := p.open(ctx)
	if err != nil {
		return robotcap.DangerousDeletePlan{}, err
	}
	defer db.Close()
	return p.plan(ctx, db, request)
}

func (p SQLiteRobotPurger) ExecuteDangerousDelete(ctx context.Context, requested robotcap.DangerousDeletePlan) (robotcap.DangerousDeleteResult, error) {
	request := robotcap.DangerousDeleteRequest{
		Mode: requested.Mode, UID: requested.UID, CID: requested.CID,
		MinUID: requested.MinUID, MaxUID: requested.MaxUID,
	}
	if err := p.validate(request); err != nil {
		return robotcap.DangerousDeleteResult{}, err
	}
	for _, uid := range requested.RegistryUIDs {
		if p.Sessions != nil {
			if err := p.Sessions.Close(uid); err != nil {
				return purgeResult(requested, false), fmt.Errorf("close 90CN robot session uid=%d: %w", uid, err)
			}
		}
	}
	db, err := p.open(ctx)
	if err != nil {
		return purgeResult(requested, false), err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return purgeResult(requested, false), fmt.Errorf("connect 90CN purge database: %w", err)
	}
	defer conn.Close()
	if err := applySQLiteWritePragmas(ctx, conn); err != nil {
		return purgeResult(requested, false), fmt.Errorf("configure 90CN purge database: %w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return purgeResult(requested, false), fmt.Errorf("begin 90CN purge: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	actual, accounts, err := p.planOnConn(ctx, tx, request)
	if err != nil {
		return purgeResult(requested, false), err
	}
	// The database may have changed between the preview and this transaction
	// (new logins, registrations or character changes). Deleting a different
	// set than the operator confirmed is never acceptable.
	if !purgePlanMatches(requested, actual) {
		return purgeResult(requested, false), fmt.Errorf(
			"90CN delete plan changed since preview: previewed accounts=%d characters=%d, current accounts=%d characters=%d; re-run the preview",
			requested.AccountCount, requested.CharacterCount, actual.AccountCount, actual.CharacterCount)
	}
	if err := executePurge(ctx, tx, request.Mode, accounts); err != nil {
		return purgeResult(actual, false), err
	}
	if err := tx.Commit(); err != nil {
		return purgeResult(actual, false), fmt.Errorf("commit 90CN purge: %w", err)
	}
	committed = true
	if len(actual.RegistryUIDs) > 0 && p.State != nil {
		if err := p.State.RemoveRobots(ctx, actual.RegistryUIDs); err != nil {
			return purgeResult(actual, true), fmt.Errorf("remove purged 90CN robot state: %w", err)
		}
	}
	// Accounts are unique per robot, so this also clears orphan identities
	// whose character name no longer matches a live robot entry (for example
	// after a failed create or a rename) and would otherwise block recreation.
	if len(actual.UIDs) > 0 && p.State != nil {
		if remover, ok := p.State.(robotstate.IdentityRemover); ok {
			accounts := make([]string, 0, len(actual.UIDs))
			for _, uid := range actual.UIDs {
				accounts = append(accounts, p.AccountPrefix+strconv.Itoa(uid))
			}
			if err := remover.RemoveIdentities(ctx, accounts); err != nil {
				return purgeResult(actual, true), fmt.Errorf("remove purged 90CN robot identities: %w", err)
			}
		}
	}
	return purgeResult(actual, actual.AccountCount > 0 || actual.CharacterCount > 0), nil
}

func (p SQLiteRobotPurger) open(ctx context.Context) (*sql.DB, error) {
	if strings.TrimSpace(p.DatabasePath) == "" {
		return nil, fmt.Errorf("90CN purge database path is required")
	}
	db, err := sql.Open("sqlite", p.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open 90CN purge database: %w", err)
	}
	configureSQLitePool(db)
	if err := applySQLiteWritePragmas(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure 90CN purge database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping 90CN purge database: %w", err)
	}
	return db, nil
}

func (p SQLiteRobotPurger) validate(request robotcap.DangerousDeleteRequest) error {
	if strings.TrimSpace(p.AccountPrefix) == "" {
		return fmt.Errorf("90CN purge account prefix is required")
	}
	switch request.Mode {
	case robotcap.DangerousDeleteModeCID:
		if request.CID <= 0 {
			return fmt.Errorf("90CN purge CID must be positive")
		}
	case robotcap.DangerousDeleteModeUID:
		if request.UID <= 0 {
			return fmt.Errorf("90CN purge UID must be positive")
		}
	case robotcap.DangerousDeleteModeRange:
		if request.MinUID <= 0 || request.MaxUID < request.MinUID {
			return fmt.Errorf("90CN purge UID range is invalid")
		}
	default:
		return fmt.Errorf("unsupported 90CN purge mode %q", request.Mode)
	}
	return nil
}

type purgeQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (p SQLiteRobotPurger) plan(ctx context.Context, query purgeQuery, request robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error) {
	plan, _, err := p.planOnConn(ctx, query, request)
	return plan, err
}

func (p SQLiteRobotPurger) planOnConn(ctx context.Context, query purgeQuery, request robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, []purgeAccount, error) {
	accounts, err := p.selectAccounts(ctx, query, request)
	if err != nil {
		return robotcap.DangerousDeletePlan{}, nil, err
	}
	plan := robotcap.DangerousDeletePlan{
		Mode: request.Mode, UID: request.UID, CID: request.CID,
		MinUID: request.MinUID, MaxUID: request.MaxUID,
	}
	for _, account := range accounts {
		plan.UIDs = append(plan.UIDs, account.uid)
		plan.CIDs = append(plan.CIDs, account.cids...)
	}
	if request.Mode == robotcap.DangerousDeleteModeCID && len(accounts) == 1 {
		plan.UID = accounts[0].uid
	}
	plan.CharacterCount = len(plan.CIDs)
	if request.Mode != robotcap.DangerousDeleteModeCID {
		plan.AccountCount = len(accounts)
	} else if len(accounts) == 1 && accounts[0].totalChars == 1 {
		plan.AccountCount = 1
	}
	plan.RegistryUIDs, err = p.registryUIDs(ctx, plan.UIDs)
	if err != nil {
		return robotcap.DangerousDeletePlan{}, nil, err
	}
	plan.RegistryCount = len(plan.RegistryUIDs)
	return plan, accounts, nil
}

func (p SQLiteRobotPurger) selectAccounts(ctx context.Context, query purgeQuery, request robotcap.DangerousDeleteRequest) ([]purgeAccount, error) {
	byName := make(map[string]*purgeAccount)
	ordered := make([]*purgeAccount, 0)

	characterRows, err := query.QueryContext(ctx,
		`SELECT c.account_id, c.character_id, (SELECT COUNT(*) FROM `+dnfCharactersTable+` cx WHERE cx.account_id=c.account_id) FROM `+
			dnfCharactersTable+` c WHERE c.account_id LIKE ? ORDER BY c.account_id, c.character_id`, p.AccountPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("scan 90CN purge candidates: %w", err)
	}
	for characterRows.Next() {
		var accountName, characterID string
		var totalChars int
		if err := characterRows.Scan(&accountName, &characterID, &totalChars); err != nil {
			characterRows.Close()
			return nil, fmt.Errorf("read 90CN purge candidate: %w", err)
		}
		uid, owned := strictRobotUID(accountName, p.AccountPrefix)
		cid := parseCharacterID(characterID)
		if !owned || cid <= 0 || !purgeRequestMatches(request, uid, cid, true) {
			continue
		}
		account := byName[accountName]
		if account == nil {
			account = &purgeAccount{name: accountName, uid: uid, totalChars: totalChars}
			byName[accountName] = account
			ordered = append(ordered, account)
		}
		account.cids = append(account.cids, cid)
	}
	if err := characterRows.Close(); err != nil {
		return nil, fmt.Errorf("close 90CN purge candidate rows: %w", err)
	}
	if err := characterRows.Err(); err != nil {
		return nil, fmt.Errorf("scan 90CN purge candidates: %w", err)
	}

	accountRows, err := query.QueryContext(ctx,
		`SELECT a.account_id FROM `+dnfAccountsTable+` a WHERE a.account_id LIKE ? AND NOT EXISTS (SELECT 1 FROM `+
			dnfCharactersTable+` c WHERE c.account_id=a.account_id)`, p.AccountPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("scan empty 90CN purge candidates: %w", err)
	}
	for accountRows.Next() {
		var accountName string
		if err := accountRows.Scan(&accountName); err != nil {
			accountRows.Close()
			return nil, fmt.Errorf("read empty 90CN purge candidate: %w", err)
		}
		uid, owned := strictRobotUID(accountName, p.AccountPrefix)
		if !owned || request.Mode == robotcap.DangerousDeleteModeCID || !purgeRequestMatches(request, uid, 0, false) {
			continue
		}
		if _, exists := byName[accountName]; exists {
			continue
		}
		account := &purgeAccount{name: accountName, uid: uid}
		byName[accountName] = account
		ordered = append(ordered, account)
	}
	if err := accountRows.Close(); err != nil {
		return nil, fmt.Errorf("close empty 90CN purge candidate rows: %w", err)
	}
	if err := accountRows.Err(); err != nil {
		return nil, fmt.Errorf("scan empty 90CN purge candidates: %w", err)
	}

	accounts := make([]purgeAccount, 0, len(ordered))
	for _, account := range ordered {
		accounts = append(accounts, *account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].name < accounts[j].name })
	return accounts, nil
}

// purgePlanMatches reports whether the transaction re-read selects exactly the
// accounts, characters and registry identities the operator previewed. The
// comparison is order-independent because both plans come from the same
// deterministic query but could be assembled in different orders.
func purgePlanMatches(requested, actual robotcap.DangerousDeletePlan) bool {
	if requested.AccountCount != actual.AccountCount || requested.CharacterCount != actual.CharacterCount {
		return false
	}
	return equalIntSets(requested.UIDs, actual.UIDs) &&
		equalIntSets(requested.CIDs, actual.CIDs) &&
		equalIntSets(requested.RegistryUIDs, actual.RegistryUIDs)
}

func equalIntSets(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]int(nil), left...)
	rightCopy := append([]int(nil), right...)
	sort.Ints(leftCopy)
	sort.Ints(rightCopy)
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func purgeRequestMatches(request robotcap.DangerousDeleteRequest, uid, cid int, hasCharacter bool) bool {
	switch request.Mode {
	case robotcap.DangerousDeleteModeCID:
		return hasCharacter && cid == request.CID
	case robotcap.DangerousDeleteModeUID:
		return uid == request.UID
	default:
		return uid >= request.MinUID && uid <= request.MaxUID
	}
}

func strictRobotUID(account, prefix string) (int, bool) {
	if !strings.HasPrefix(account, prefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(account, prefix)
	uid, err := strconv.Atoi(suffix)
	return uid, err == nil && uid > 0 && account == prefix+strconv.Itoa(uid)
}

func (p SQLiteRobotPurger) registryUIDs(ctx context.Context, selected []int) ([]int, error) {
	if p.State == nil || len(selected) == 0 {
		return nil, nil
	}
	robots, err := p.State.SelectRobots(ctx, robotcap.CommandRequest{Count: int(^uint(0) >> 1)})
	if err != nil {
		return nil, fmt.Errorf("read 90CN robot state for purge: %w", err)
	}
	wanted := make(map[int]struct{}, len(selected))
	for _, uid := range selected {
		wanted[uid] = struct{}{}
	}
	result := make([]int, 0)
	for _, robot := range robots {
		if _, ok := wanted[robot.UID]; ok {
			result = append(result, robot.UID)
		}
	}
	sort.Ints(result)
	return result, nil
}

func executePurge(ctx context.Context, tx *sql.Tx, mode string, accounts []purgeAccount) error {
	if len(accounts) == 0 {
		return nil
	}
	if mode == robotcap.DangerousDeleteModeCID {
		account := accounts[0]
		if len(account.cids) != 1 {
			return fmt.Errorf("90CN CID purge candidate is inconsistent")
		}
		if err := deleteCharacterRows(ctx, tx, strconv.Itoa(account.cids[0])); err != nil {
			return err
		}
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+dnfCharactersTable+` WHERE account_id=?`, account.name).Scan(&remaining); err != nil {
			return fmt.Errorf("count remaining 90CN account %s characters: %w", account.name, err)
		}
		if remaining == 0 {
			if err := deleteAccountRows(ctx, tx, account.name); err != nil {
				return err
			}
		}
		return nil
	}
	for _, account := range accounts {
		if err := deleteAccountRows(ctx, tx, account.name); err != nil {
			return err
		}
	}
	return nil
}

func purgeResult(plan robotcap.DangerousDeletePlan, deleted bool) robotcap.DangerousDeleteResult {
	return robotcap.DangerousDeleteResult{
		Mode: plan.Mode, UID: plan.UID, CID: plan.CID, MinUID: plan.MinUID, MaxUID: plan.MaxUID,
		AccountCount: plan.AccountCount, CharacterCount: plan.CharacterCount, RegistryCount: plan.RegistryCount,
		Deleted: deleted,
	}
}
