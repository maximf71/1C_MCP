package metadata

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mcp-1c-analog/internal/designer"
)

var planIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type Manager struct {
	Designer *designer.Client
	WorkDir  string
	mu       sync.Mutex
}

type Plan struct {
	ID                string    `json:"plan_id"`
	MetadataType      string    `json:"metadata_type"`
	SourceName        string    `json:"source_name"`
	TargetName        string    `json:"target_name"`
	SourceFingerprint string    `json:"source_fingerprint"`
	CanonicalDump     string    `json:"canonical_dump"`
	PreparedAt        time.Time `json:"prepared_at"`
	State             string    `json:"state"`
	ChangedFiles      []string  `json:"changed_files"`
	UUIDsRegenerated  int       `json:"uuids_regenerated"`
	BackupCF          string    `json:"backup_cf,omitempty"`
}

type ApplyResult struct {
	PlanID       string `json:"plan_id"`
	State        string `json:"state"`
	BackupCF     string `json:"backup_cf"`
	VerifiedType string `json:"verified_type"`
	VerifiedName string `json:"verified_name"`
}

func NewManager(client *designer.Client, workDir string) *Manager {
	return &Manager{Designer: client, WorkDir: workDir}
}

func (m *Manager) Status(ctx context.Context, probe bool) designer.Status {
	return m.Designer.Status(ctx, probe)
}

func (m *Manager) List(ctx context.Context) ([]Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dump, cleanup, err := m.freshDump(ctx, "list")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	objects, err := Discover(dump)
	if err != nil {
		return nil, err
	}
	for index := range objects {
		objects[index].XMLPath = ""
		objects[index].CompanionPath = ""
	}
	return objects, nil
}

func (m *Manager) Inspect(ctx context.Context, typeName, name string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dump, cleanup, err := m.freshDump(ctx, "inspect")
	if err != nil {
		return Object{}, err
	}
	defer cleanup()
	object, err := Inspect(dump, typeName, name)
	if err != nil {
		return Object{}, err
	}
	object.XMLPath = filepath.Base(object.XMLPath)
	if object.CompanionPath != "" {
		object.CompanionPath = filepath.Base(object.CompanionPath)
	}
	return object, nil
}

func (m *Manager) Verify(ctx context.Context, typeName, name string) (Object, error) {
	return m.Inspect(ctx, typeName, name)
}

func (m *Manager) Prepare(ctx context.Context, typeName, sourceName, targetName string) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ValidIdentifier(typeName) || !ValidIdentifier(sourceName) || !ValidIdentifier(targetName) {
		return Plan{}, errors.New("metadata type and names must be valid 1C identifiers")
	}
	if sourceName == targetName {
		return Plan{}, errors.New("source and target names must differ")
	}
	if err := m.ensureLayout(); err != nil {
		return Plan{}, err
	}
	planID, err := randomPlanID()
	if err != nil {
		return Plan{}, err
	}
	planDir := filepath.Join(m.WorkDir, "plans", planID)
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		return Plan{}, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(planDir)
		}
	}()
	stagedDump := filepath.Join(planDir, "staged")
	if err := m.Designer.DumpConfig(ctx, stagedDump); err != nil {
		return Plan{}, err
	}
	fingerprint, err := Fingerprint(stagedDump)
	if err != nil {
		return Plan{}, err
	}
	cloneResult, err := Clone(stagedDump, typeName, sourceName, targetName)
	if err != nil {
		return Plan{}, err
	}
	validationBase := filepath.Join(planDir, "validation-base")
	validationClient := m.Designer.ForInfobase(validationBase)
	if err := validationClient.CreateInfobase(ctx, validationBase); err != nil {
		return Plan{}, fmt.Errorf("create validation infobase: %w", err)
	}
	if err := validationClient.LoadConfigFromFiles(ctx, stagedDump); err != nil {
		return Plan{}, fmt.Errorf("validate LoadConfigFromFiles: %w", err)
	}
	if err := validationClient.UpdateDBCfg(ctx); err != nil {
		return Plan{}, fmt.Errorf("validate UpdateDBCfg: %w", err)
	}
	canonicalDump := filepath.Join(planDir, "canonical")
	if err := validationClient.DumpConfig(ctx, canonicalDump); err != nil {
		return Plan{}, fmt.Errorf("canonical DumpConfigToFiles: %w", err)
	}
	if err := Equivalent(canonicalDump, typeName, sourceName, targetName); err != nil {
		return Plan{}, fmt.Errorf("clone equivalence verification failed: %w", err)
	}
	plan := Plan{
		ID:                planID,
		MetadataType:      typeName,
		SourceName:        sourceName,
		TargetName:        targetName,
		SourceFingerprint: fingerprint,
		CanonicalDump:     canonicalDump,
		PreparedAt:        time.Now().UTC(),
		State:             "prepared",
		ChangedFiles:      cloneResult.ChangedFiles,
		UUIDsRegenerated:  len(cloneResult.UUIDMap),
	}
	if err := m.savePlan(plan); err != nil {
		return Plan{}, err
	}
	if err := os.RemoveAll(stagedDump); err != nil {
		return Plan{}, err
	}
	if err := os.RemoveAll(validationBase); err != nil {
		return Plan{}, err
	}
	succeeded = true
	return plan, nil
}

func (m *Manager) Apply(ctx context.Context, planID string) (ApplyResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plan, err := m.loadPlan(planID)
	if err != nil {
		return ApplyResult{}, err
	}
	if plan.State != "prepared" {
		return ApplyResult{}, fmt.Errorf("plan %s is in state %s, expected prepared", planID, plan.State)
	}
	currentDump, cleanup, err := m.freshDump(ctx, "apply-check")
	if err != nil {
		return ApplyResult{}, err
	}
	currentFingerprint, err := Fingerprint(currentDump)
	cleanup()
	if err != nil {
		return ApplyResult{}, err
	}
	if err := validateFingerprint(plan.SourceFingerprint, currentFingerprint); err != nil {
		return ApplyResult{}, err
	}
	planDir, _ := m.planDir(planID)
	backupPath := filepath.Join(planDir, "backup-before-apply.cf")
	if err := m.Designer.DumpCfg(ctx, backupPath); err != nil {
		return ApplyResult{}, fmt.Errorf("configuration backup failed: %w", err)
	}
	plan.BackupCF = backupPath
	if err := m.savePlan(plan); err != nil {
		return ApplyResult{}, err
	}
	if err := m.Designer.LoadConfigFromFiles(ctx, plan.CanonicalDump); err != nil {
		return ApplyResult{}, m.rollback(ctx, plan, fmt.Errorf("LoadConfigFromFiles failed: %w", err))
	}
	if err := m.Designer.UpdateDBCfg(ctx); err != nil {
		return ApplyResult{}, m.rollback(ctx, plan, fmt.Errorf("UpdateDBCfg failed: %w", err))
	}
	verificationDump, verificationCleanup, err := m.freshDump(ctx, "apply-verify")
	if err != nil {
		return ApplyResult{}, err
	}
	_, verifyErr := Find(verificationDump, plan.MetadataType, plan.TargetName)
	verificationCleanup()
	if verifyErr != nil {
		return ApplyResult{}, m.rollback(ctx, plan, fmt.Errorf("post-apply verification failed: %w", verifyErr))
	}
	plan.State = "applied"
	if err := m.savePlan(plan); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{
		PlanID:       plan.ID,
		State:        plan.State,
		BackupCF:     backupPath,
		VerifiedType: plan.MetadataType,
		VerifiedName: plan.TargetName,
	}, nil
}

func validateFingerprint(expected, actual string) error {
	if expected == "" || actual == "" {
		return errors.New("configuration fingerprint is missing")
	}
	if expected != actual {
		return errors.New("configuration changed after plan preparation; prepare a new plan")
	}
	return nil
}

func (m *Manager) Discard(planID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	plan, err := m.loadPlan(planID)
	if err != nil {
		return err
	}
	if plan.State != "prepared" {
		return fmt.Errorf("only prepared plans can be discarded; current state is %s", plan.State)
	}
	planDir, err := m.planDir(planID)
	if err != nil {
		return err
	}
	return os.RemoveAll(planDir)
}

func (m *Manager) rollback(ctx context.Context, plan Plan, cause error) error {
	restoreErr := m.Designer.LoadCfg(ctx, plan.BackupCF)
	if restoreErr == nil {
		restoreErr = m.Designer.UpdateDBCfg(ctx)
	}
	if restoreErr != nil {
		plan.State = "rollback_failed"
		_ = m.savePlan(plan)
		return fmt.Errorf("%v; automatic rollback failed: %w; backup: %s", cause, restoreErr, plan.BackupCF)
	}
	plan.State = "rolled_back"
	_ = m.savePlan(plan)
	return fmt.Errorf("%v; configuration was restored from %s", cause, plan.BackupCF)
}

func (m *Manager) freshDump(ctx context.Context, prefix string) (string, func(), error) {
	if err := m.ensureLayout(); err != nil {
		return "", func() {}, err
	}
	root, err := os.MkdirTemp(filepath.Join(m.WorkDir, "tmp"), prefix+"-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	dump := filepath.Join(root, "dump")
	if err := m.Designer.DumpConfig(ctx, dump); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return dump, cleanup, nil
}

func (m *Manager) ensureLayout() error {
	for _, path := range []string{m.WorkDir, filepath.Join(m.WorkDir, "plans"), filepath.Join(m.WorkDir, "tmp")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) savePlan(plan Plan) error {
	planDir, err := m.planDir(plan.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(planDir, "manifest.json.tmp")
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(planDir, "manifest.json"))
}

func (m *Manager) loadPlan(planID string) (Plan, error) {
	planDir, err := m.planDir(planID)
	if err != nil {
		return Plan{}, err
	}
	data, err := os.ReadFile(filepath.Join(planDir, "manifest.json"))
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return Plan{}, err
	}
	if plan.ID != planID {
		return Plan{}, errors.New("plan manifest ID does not match directory")
	}
	return plan, nil
}

func (m *Manager) planDir(planID string) (string, error) {
	if !planIDPattern.MatchString(planID) {
		return "", errors.New("invalid plan_id")
	}
	base := filepath.Clean(filepath.Join(m.WorkDir, "plans"))
	path := filepath.Clean(filepath.Join(base, planID))
	if !strings.HasPrefix(strings.ToLower(path), strings.ToLower(base)+string(os.PathSeparator)) {
		return "", errors.New("plan path escapes work directory")
	}
	return path, nil
}

func randomPlanID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func ObjectTypes(objects []Object) []string {
	set := map[string]struct{}{}
	for _, object := range objects {
		set[object.Type] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
