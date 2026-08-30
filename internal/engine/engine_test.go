package engine

import (
	"errors"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

type fakeAdapter struct {
	id        model.ToolID
	installed bool
	version   string
	detectErr error
	inv       model.Inventory
	scanErr   error
	scanned   bool
}

func (f *fakeAdapter) ID() model.ToolID { return f.id }
func (f *fakeAdapter) Detect() (bool, string, error) {
	return f.installed, f.version, f.detectErr
}
func (f *fakeAdapter) Scan(scope model.ScanScope) (model.Inventory, error) {
	f.scanned = true
	return f.inv, f.scanErr
}

func TestScanAll(t *testing.T) {
	installed := &fakeAdapter{
		id: model.ToolClaudeCode, installed: true, version: "2.0.44",
		inv: model.Inventory{
			Tool: model.ToolClaudeCode,
			Components: []model.Component{
				model.Skill{Spec: model.SkillSpec{Name: "brainstorming", Scope: model.ScopeGlobal}},
			},
		},
	}
	absent := &fakeAdapter{id: model.ToolCodex, installed: false}

	results := ScanAll([]Adapter{installed, absent}, model.ScanScope{Global: true})

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (one per adapter, installed or not)", len(results))
	}
	if !results[0].Installed || results[0].Version != "2.0.44" || results[0].Err != nil {
		t.Errorf("installed result = %+v", results[0])
	}
	if len(results[0].Inventory.Components) != 1 {
		t.Errorf("installed inventory = %+v", results[0].Inventory)
	}
	if results[1].Installed {
		t.Errorf("absent tool reported installed")
	}
	if absent.scanned {
		t.Error("Scan() was called on a tool that is not installed")
	}
	if results[0].Tool != model.ToolClaudeCode || results[1].Tool != model.ToolCodex {
		t.Errorf("result order should follow adapter order: %v, %v", results[0].Tool, results[1].Tool)
	}
}

func TestScanAllDetectError(t *testing.T) {
	boom := &fakeAdapter{id: model.ToolClaudeCode, detectErr: errors.New("perm denied")}
	results := ScanAll([]Adapter{boom}, model.ScanScope{Global: true})
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Err == nil {
		t.Error("detect error must surface in the result, not vanish")
	}
	if boom.scanned {
		t.Error("Scan() must not run after a failed Detect()")
	}
}

func TestScanAllScanError(t *testing.T) {
	boom := &fakeAdapter{
		id: model.ToolCodex, installed: true, version: "0.45.0",
		scanErr: errors.New("io failure"),
	}
	results := ScanAll([]Adapter{boom}, model.ScanScope{Global: true})
	if results[0].Err == nil {
		t.Error("scan error must surface in the result")
	}
	if !results[0].Installed {
		t.Error("a failed scan must not erase the detection result")
	}
}
