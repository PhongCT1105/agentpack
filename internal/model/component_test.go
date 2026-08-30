package model

import "testing"

// Concrete component types must satisfy Component.
var _ Component = Skill{}

func TestSkillComponent(t *testing.T) {
	s := Skill{Spec: SkillSpec{
		Name:        "brainstorming",
		Scope:       ScopeGlobal,
		Dir:         "/home/user/.claude/skills/brainstorming",
		Description: "Explore intent before implementation",
	}}
	if s.Kind() != KindSkill {
		t.Errorf("Skill.Kind() = %q, want %q", s.Kind(), KindSkill)
	}
	if s.Name() != "brainstorming" {
		t.Errorf("Skill.Name() = %q, want %q", s.Name(), "brainstorming")
	}
	if s.Scope() != ScopeGlobal {
		t.Errorf("Skill.Scope() = %q, want %q", s.Scope(), ScopeGlobal)
	}
}
