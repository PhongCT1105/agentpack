package model

// ScanScope selects what a Scan covers. Zero value scans nothing.
type ScanScope struct {
	Global     bool   // scan user-level config (~)
	ProjectDir string // if non-empty, scan this project directory
}

// SkillSpec is the data of a scanned skill. Skill wraps it to satisfy
// Component; the Spec field keeps plain, literal-friendly data separate from
// the interface methods (whose names would collide with field names).
type SkillSpec struct {
	Name        string
	Scope       Scope
	Dir         string // absolute path of the directory containing SKILL.md
	Description string // from SKILL.md frontmatter, may be empty
}

// Skill is a neutral skill component (a directory with a SKILL.md).
type Skill struct {
	Spec SkillSpec
}

func (s Skill) Kind() Kind   { return KindSkill }
func (s Skill) Name() string { return s.Spec.Name }
func (s Skill) Scope() Scope { return s.Spec.Scope }
