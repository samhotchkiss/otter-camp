package bootstrap

import (
	"os"
	"strings"
)

const (
	defaultOrgSlug   = "default"
	defaultOrgName   = "OtterCamp"
	defaultSkillsDir = "./skills"
)

type Config struct {
	OrgSlug       string
	OrgName       string
	AdminEmail    string
	AdminPassword string
	SkillsDir     string
}

func ConfigFromEnv() Config {
	return ConfigFromLookup(os.LookupEnv)
}

func ConfigFromLookup(lookup func(string) (string, bool)) Config {
	if lookup == nil {
		lookup = func(string) (string, bool) {
			return "", false
		}
	}

	cfg := Config{
		OrgSlug:       defaultOrgSlug,
		OrgName:       defaultOrgName,
		AdminEmail:    "",
		AdminPassword: "",
		SkillsDir:     defaultSkillsDir,
	}

	if value, ok := lookup("OTTERCAMP_ORG_SLUG"); ok && strings.TrimSpace(value) != "" {
		cfg.OrgSlug = strings.TrimSpace(value)
	}
	if value, ok := lookup("OTTERCAMP_ORG_NAME"); ok && strings.TrimSpace(value) != "" {
		cfg.OrgName = strings.TrimSpace(value)
	}
	if value, ok := lookup("OTTERCAMP_ADMIN_EMAIL"); ok && strings.TrimSpace(value) != "" {
		cfg.AdminEmail = strings.TrimSpace(value)
	}
	if value, ok := lookup("OTTERCAMP_ADMIN_PASSWORD"); ok && strings.TrimSpace(value) != "" {
		cfg.AdminPassword = value
	}
	if value, ok := lookup("OTTERCAMP_SKILLS_DIR"); ok && strings.TrimSpace(value) != "" {
		cfg.SkillsDir = strings.TrimSpace(value)
	}

	return cfg
}
