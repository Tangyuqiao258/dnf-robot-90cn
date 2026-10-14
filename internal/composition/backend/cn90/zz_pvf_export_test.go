package cn90

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// zz_pvf_export_test.go is the standing PVF export tool. PVF forensics should
// run against an exported tree with plain grep instead of a new one-off probe
// per question: this writes the archive's text files to a directory tree that
// keeps their archive paths.
//
//	CN90_PVF_EXPORT_DIR    output root (default %TEMP%\pvf-dump)
//	CN90_PVF_EXPORT_PREFIX archive path prefix filter (default "skill/"; empty = all)
//	CN90_PVF_EXPORT_EXT    comma-separated extension filter (default ".lst"; empty = all)
//	CN90_PVF_EXPORT_DRYRUN 1 = report counts and sizes without writing
func TestZZPVFExport(t *testing.T) {
	if os.Getenv("CN90_PVF_EXPORT") == "" && os.Getenv("CN90_PVF_EXPORT_DRYRUN") == "" {
		t.Skip("set CN90_PVF_EXPORT=1 (or CN90_PVF_EXPORT_DRYRUN=1)")
	}
	root := liveRuntimeRoot(t)
	layout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	pvfPath, _, err := layout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	prefix := os.Getenv("CN90_PVF_EXPORT_PREFIX")
	if prefix == "" && os.Getenv("CN90_PVF_EXPORT_PREFIX") == "" && os.Getenv("CN90_PVF_EXPORT_ALL") == "" {
		prefix = "skill/"
	}
	exts := []string{".lst"}
	if raw := os.Getenv("CN90_PVF_EXPORT_EXT"); raw != "" {
		exts = strings.Split(raw, ",")
	}
	if os.Getenv("CN90_PVF_EXPORT_ALL") != "" {
		prefix, exts = "", nil
	}
	outDir := os.Getenv("CN90_PVF_EXPORT_DIR")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "pvf-dump")
	}
	dryRun := os.Getenv("CN90_PVF_EXPORT_DRYRUN") != ""

	paths := make([]string, 0, len(archive.paths))
	for p := range archive.paths {
		if prefix != "" && !strings.HasPrefix(p, prefix) {
			continue
		}
		if len(exts) > 0 {
			ok := false
			for _, ext := range exts {
				if strings.HasSuffix(p, ext) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// Size is only known for the script/utf16 file kinds; those are the ones
	// ReadText can decode.
	var totalBytes, textFiles, skipped int
	for _, p := range paths {
		index, ok := archive.paths[p]
		if !ok {
			continue
		}
		item := archive.files[index]
		if item.dataType != 1 && item.dataType != 3 {
			skipped++
			continue
		}
		totalBytes += item.size
		textFiles++
	}
	t.Logf("prefix=%q exts=%v candidates=%d decodable=%d (%.1f MB) non-text=%d dryRun=%v",
		prefix, exts, len(paths), textFiles, float64(totalBytes)/(1<<20), skipped, dryRun)
	if dryRun {
		return
	}
	written, writeErr := 0, 0
	for _, p := range paths {
		body, err := archive.ReadText(p)
		if err != nil || body == "" {
			continue
		}
		target := filepath.Join(outDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			writeErr++
			continue
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			writeErr++
			continue
		}
		written++
	}
	t.Logf("exported %d files to %s (%d write errors)", written, outDir, writeErr)
}
