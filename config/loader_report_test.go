package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// captureRepairStderr redirects stderr during RepairLayerFile calls so tests
// stay quiet and can assert the warnings (same trick as captureStderr).
func captureRepairStderr(t *testing.T) (restore func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	var once sync.Once
	var out string
	return func() string {
		once.Do(func() {
			os.Stderr = orig
			_ = w.Close()
			b, _ := io.ReadAll(r)
			_ = r.Close()
			out = string(b)
		})
		return out
	}
}

// isolatedHome points HOME at a temp dir so no real user config leaks in.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".goa"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return home
}

func homeConfigPath(home string) string {
	return filepath.Join(home, ".goa", "config.yaml")
}

func TestLoadWithReport_FallsBackToDefaultsOnGarbageStallValue(t *testing.T) {
	home := isolatedHome(t)
	if err := os.WriteFile(homeConfigPath(home), []byte("execution:\n  activity_timeout: \"abc\"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cl := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, rep, err := cl.LoadWithReport()
	if err != nil {
		t.Fatalf("LoadWithReport must not fail hard, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg must never be nil when defaults can load")
	}
	if !rep.UsedDefaults {
		t.Errorf("UsedDefaults = false, want true")
	}
	if rep.FallbackErr == nil || !strings.Contains(rep.FallbackErr.Error(), "activity_timeout") {
		t.Errorf("FallbackErr must name activity_timeout, got: %v", rep.FallbackErr)
	}
	if got := cfg.Execution.ActivityTimeout; got != "5m" {
		t.Errorf("fallback config must carry the default window, got %q", got)
	}
}

func TestLoadWithReport_HealsBareStallValues(t *testing.T) {
	home := isolatedHome(t)
	if err := os.WriteFile(homeConfigPath(home), []byte("execution:\n  activity_timeout: 60\n  activity_warn_after: 30\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	restore := captureStderr(t)
	cl := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, rep, err := cl.LoadWithReport()
	captured := restore()
	if err != nil {
		t.Fatalf("LoadWithReport: %v", err)
	}
	if rep.UsedDefaults || rep.FallbackErr != nil {
		t.Errorf("bare values heal in memory, no fallback expected: %+v", rep)
	}
	if len(rep.Healed) != 2 {
		t.Fatalf("Healed must carry one record per corrected key, got: %+v", rep.Healed)
	}
	requireDescribedHeals(t, rep.Healed)
	if cfg.Execution.ActivityTimeout != "60s" || cfg.Execution.ActivityWarnAfter != "30s" {
		t.Errorf("healed values = %q/%q, want 60s/30s", cfg.Execution.ActivityTimeout, cfg.Execution.ActivityWarnAfter)
	}
	if !strings.Contains(captured, "Warning:") {
		t.Errorf("heal must warn on stderr, got:\n%s", captured)
	}
}

// requireDescribedHeals asserts every heal record names the file, the offending
// value, the correction and a reason — the UI renders them verbatim, so none of
// them may be empty.
func requireDescribedHeals(t *testing.T, heals []Heal) {
	t.Helper()
	for _, h := range heals {
		if !strings.HasSuffix(h.Source, "config.yaml") {
			t.Errorf("Healed Source = %q, want the home config path", h.Source)
		}
		if h.Bad == "" || h.Fixed == "" || h.Reason == "" {
			t.Errorf("heal record must carry bad/fixed/reason, got: %+v", h)
		}
		if h.Describe() == "" {
			t.Errorf("Describe must render a message, got %+v", h)
		}
	}
}

// TestLoadWithReport_DropsUnparseableYAMLLayer pins that a layer that cannot be
// parsed is dropped with a visible warning instead of aborting startup.
func TestLoadWithReport_DropsUnparseableYAMLLayer(t *testing.T) {
	home := isolatedHome(t)
	if err := os.WriteFile(homeConfigPath(home), []byte("{[}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	restore := captureStderr(t)
	cl := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, rep, err := cl.LoadWithReport()
	captured := restore()
	if err != nil {
		t.Fatalf("bad layer must be dropped, not fatal, got: %v", err)
	}
	if len(rep.Dropped) != 1 {
		t.Fatalf("Dropped = %v, want exactly one entry", rep.Dropped)
	}
	if !strings.HasSuffix(rep.Dropped[0].Source, "config.yaml") {
		t.Errorf("Dropped[0].Source = %q, want the home config path", rep.Dropped[0].Source)
	}
	if cfg.Execution.ActivityTimeout != "5m" {
		t.Errorf("defaults must apply for the dropped layer, got %q", cfg.Execution.ActivityTimeout)
	}
	if !strings.Contains(captured, "Warning: ignoring invalid config") {
		t.Errorf("drop must warn on stderr, got:\n%s", captured)
	}
}

func TestLoadWithReport_CleanLoadIsEmptyReport(t *testing.T) {
	home := isolatedHome(t)
	if err := os.WriteFile(homeConfigPath(home), []byte("execution:\n  activity_timeout: 2m\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cl := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, rep, err := cl.LoadWithReport()
	if err != nil {
		t.Fatalf("LoadWithReport: %v", err)
	}
	if !rep.Empty() {
		t.Errorf("clean load must produce an empty report, got: %+v", rep)
	}
	if cfg.Execution.ActivityTimeout != "2m" {
		t.Errorf("user value must survive, got %q", cfg.Execution.ActivityTimeout)
	}
}

func repairHomeWith(t *testing.T, yamlText string) (cl *CascadeLoader, path string, original []byte) {
	t.Helper()
	home := isolatedHome(t)
	path = homeConfigPath(home)
	if err := os.WriteFile(path, []byte(yamlText), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return NewCascadeLoader(t.TempDir(), "", nil), path, []byte(yamlText)
}

func TestRepairLayerFile_BarePairRepairedWithBackup(t *testing.T) {
	cl, path, original := repairHomeWith(t, "execution:\n  activity_timeout: \"60\"\n  activity_warn_after: \"30\"\n")
	restore := captureRepairStderr(t)
	backup, err := cl.RepairLayerFile(path)
	captured := restore()
	if err != nil {
		t.Fatalf("RepairLayerFile: %v", err)
	}
	if backup == "" {
		t.Fatal("backup path must be reported for a repaired file")
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("backup must hold the original bytes:\n%s\ngot:\n%s", original, got)
	}
	repaired, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repaired: %v", err)
	}
	for _, want := range []string{"60s", "30s"} {
		if !strings.Contains(string(repaired), want) {
			t.Errorf("repaired file must contain %q, got:\n%s", want, repaired)
		}
	}
	if strings.Contains(captured, "not a duration") {
		t.Errorf("bare values are healed, not removed, got:\n%s", captured)
	}
	// The repaired file must load cleanly with the user's values.
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("repaired file must load: %v", err)
	}
	if cfg.Execution.ActivityTimeout != "60s" || cfg.Execution.ActivityWarnAfter != "30s" {
		t.Errorf("loaded values = %q/%q, want 60s/30s", cfg.Execution.ActivityTimeout, cfg.Execution.ActivityWarnAfter)
	}
}

func TestRepairLayerFile_RemovesGarbageStallKey(t *testing.T) {
	cl, path, _ := repairHomeWith(t, "execution:\n  activity_timeout: \"abc\"\n  retries: 3\n")
	restore := captureRepairStderr(t)
	backup, err := cl.RepairLayerFile(path)
	captured := restore()
	if err != nil {
		t.Fatalf("RepairLayerFile: %v", err)
	}
	if backup == "" {
		t.Fatal("backup path must be reported")
	}
	repaired, _ := os.ReadFile(path)
	if strings.Contains(string(repaired), "activity_timeout") {
		t.Errorf("garbage key must be removed, got:\n%s", repaired)
	}
	if !strings.Contains(string(repaired), "retries: 3") {
		t.Errorf("unrelated keys must survive, got:\n%s", repaired)
	}
	if !strings.Contains(captured, "removing the key") {
		t.Errorf("removal must warn, got:\n%s", captured)
	}
	// The key's default now applies on load.
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("repaired file must load: %v", err)
	}
	if cfg.Execution.ActivityTimeout != "5m" {
		t.Errorf("default must apply after key removal, got %q", cfg.Execution.ActivityTimeout)
	}
}

func TestRepairLayerFile_UnparseableYAMLBecomesEmptyMapping(t *testing.T) {
	cl, path, _ := repairHomeWith(t, "{[}\n")
	restore := captureRepairStderr(t)
	backup, err := cl.RepairLayerFile(path)
	restore()
	if err != nil {
		t.Fatalf("RepairLayerFile: %v", err)
	}
	if backup == "" {
		t.Fatal("backup path must be reported")
	}
	repaired, _ := os.ReadFile(path)
	if strings.TrimSpace(string(repaired)) != "{}" {
		t.Errorf("unparseable YAML must become an empty mapping, got:\n%s", repaired)
	}
	if _, err := cl.Load(); err != nil {
		t.Fatalf("repaired file must load: %v", err)
	}
}

func TestRepairLayerFile_DropsContradictoryWarn(t *testing.T) {
	cl, path, _ := repairHomeWith(t, "execution:\n  activity_timeout: 45s\n  activity_warn_after: 45s\n")
	restore := captureRepairStderr(t)
	backup, err := cl.RepairLayerFile(path)
	captured := restore()
	if err != nil {
		t.Fatalf("RepairLayerFile: %v", err)
	}
	if backup == "" {
		t.Fatal("backup path must be reported")
	}
	repaired, _ := os.ReadFile(path)
	if strings.Contains(string(repaired), "activity_warn_after") {
		t.Errorf("contradictory warn key must be removed, got:\n%s", repaired)
	}
	if !strings.Contains(captured, "dropping the stale override") {
		t.Errorf("pair fix must warn, got:\n%s", captured)
	}
	if _, err := cl.Load(); err != nil {
		t.Fatalf("repaired file must load: %v", err)
	}
}

func TestRepairLayerFile_ValidFileUntouched(t *testing.T) {
	cl, path, original := repairHomeWith(t, "execution:\n  activity_timeout: 2m\n")
	restore := captureRepairStderr(t)
	backup, err := cl.RepairLayerFile(path)
	restore()
	if err != nil {
		t.Fatalf("RepairLayerFile: %v", err)
	}
	if backup != "" {
		t.Errorf("valid file must not be rewritten, backup = %q", backup)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Errorf("valid file must stay byte-identical:\n%s\ngot:\n%s", original, got)
	}
	matches, _ := filepath.Glob(path + ".bak-*")
	if len(matches) != 0 {
		t.Errorf("no backup must be created for a valid file, got %v", matches)
	}
}
