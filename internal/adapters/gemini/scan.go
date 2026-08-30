package gemini

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// Scan reads Gemini CLI configuration in the requested scopes into the
// neutral model. It never writes.
//
// It opens only the paths the config matrix models — ~/.gemini/settings.json,
// ~/.gemini/GEMINI.md, ~/.gemini/extensions/, and their project-scope
// counterparts. The matrix's "never port" list (~/.gemini/oauth_creds.json,
// .env files, caches) is not read, not enumerated, and not reported: a
// warning naming a credential file is still a leak of where credentials
// live, so those paths are outside the scan surface entirely.
func (a *Adapter) Scan(scope model.ScanScope) (model.Inventory, error) {
	inv := model.Inventory{Tool: model.ToolGeminiCLI}

	if scope.Global && a.home != "" {
		root := filepath.Join(a.home, ".gemini")
		steps := []func() error{
			func() error {
				return a.scanSettingsFile(&inv, filepath.Join(root, "settings.json"), model.ScopeGlobal)
			},
			func() error { return scanRuleFile(&inv, filepath.Join(root, "GEMINI.md"), model.ScopeGlobal) },
			func() error { return scanExtensions(&inv, filepath.Join(root, "extensions")) },
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return inv, err
			}
		}
	}
	if scope.ProjectDir != "" {
		proj := scope.ProjectDir
		steps := []func() error{
			func() error {
				return a.scanSettingsFile(&inv, filepath.Join(proj, ".gemini", "settings.json"), model.ScopeProject)
			},
			// Gemini CLI also reads GEMINI.md files nested deeper in a repo,
			// but agentpack models the root one only — walking a whole
			// project tree is not a scan's job.
			func() error { return scanRuleFile(&inv, filepath.Join(proj, "GEMINI.md"), model.ScopeProject) },
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return inv, err
			}
		}
	}

	return inv, nil
}

// scanRuleFile models one instruction file (GEMINI.md) if present.
func scanRuleFile(inv *model.Inventory, path string, scope model.Scope) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path: path, Message: "expected a file, found a directory; skipped",
		})
		return nil
	}
	if abs, absErr := filepath.Abs(path); absErr == nil {
		path = abs
	}
	inv.Components = append(inv.Components, model.Rule{Spec: model.RuleSpec{
		Name: filepath.Base(path), Scope: scope, Path: path,
	}})
	return nil
}
