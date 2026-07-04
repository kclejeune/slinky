package render

import (
	"os"
	"text/template"
	"text/template/parse"

	"github.com/kclejeune/slinky/internal/config"
)

// parseForExtraction parses the template referenced by cfg and returns it,
// or nil on any error (parse failure, missing template, command mode).
func parseForExtraction(name string, cfg *config.FileConfig) *template.Template {
	if cfg.Render == "command" || cfg.Template == "" {
		return nil
	}

	tplPath := config.ExpandPath(cfg.Template)
	tplData, err := os.ReadFile(tplPath)
	if err != nil {
		return nil
	}

	funcMap, err := buildFuncMap(nil, nil, "")
	if err != nil {
		return nil
	}

	tmpl := template.New(name).Funcs(funcMap)
	if len(cfg.Delims) == 2 {
		tmpl = tmpl.Delims(cfg.Delims[0], cfg.Delims[1])
	}
	tmpl, err = tmpl.Parse(string(tplData))
	if err != nil {
		return nil
	}
	return tmpl
}

// walkTemplates visits every command node in every associated template.
func walkTemplates(tmpl *template.Template, visit func(*parse.CommandNode)) {
	for _, t := range tmpl.Templates() {
		if t.Tree != nil && t.Root != nil {
			walkNode(t.Root, visit)
		}
	}
}

// ExtractEnvVars parses the template referenced by cfg and walks its AST to
// find all statically-referenced environment variable names (via env "KEY" and
// envDefault "KEY" "fallback" calls). Returns the set of referenced var names,
// or nil on any error (parse failure, missing template, command mode) as a
// "keep all env" fallback.
func ExtractEnvVars(name string, cfg *config.FileConfig) map[string]bool {
	tmpl := parseForExtraction(name, cfg)
	if tmpl == nil {
		return nil
	}

	vars := make(map[string]bool)
	walkTemplates(tmpl, func(cmd *parse.CommandNode) {
		collectEnvVar(cmd, vars)
	})
	return vars
}

// providerFuncNames are the secret-manager integration template functions.
var providerFuncNames = map[string]bool{
	"fnox": true, "secretspec": true, "op": true,
}

// ExtractProviderFuncs reports which secret-manager template functions
// (fnox, secretspec, op) the template references. Returns nil on any parse
// error or for command-mode files.
func ExtractProviderFuncs(name string, cfg *config.FileConfig) map[string]bool {
	tmpl := parseForExtraction(name, cfg)
	if tmpl == nil {
		return nil
	}

	used := make(map[string]bool)
	walkTemplates(tmpl, func(cmd *parse.CommandNode) {
		if len(cmd.Args) == 0 {
			return
		}
		if ident, ok := cmd.Args[0].(*parse.IdentifierNode); ok &&
			providerFuncNames[ident.Ident] {
			used[ident.Ident] = true
		}
	})
	return used
}

var cmdEnvAllowlist = map[string]bool{
	"HOME": true, "USER": true, "LOGNAME": true, "PATH": true,
	"SHELL": true, "TERM": true, "LANG": true,
}

// FilterEnv returns a filtered copy of env containing only the variables
// referenced by cfg's template. Returns nil if env is nil (global layer).
// For command-mode files, returns only the allowlist vars (PATH, HOME, etc.)
// since referenced variables cannot be statically extracted. Returns the
// original env unchanged if extraction fails (safe fallback).
func FilterEnv(name string, cfg *config.FileConfig, env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	if cfg.Render == "command" {
		filtered := make(map[string]string, len(cmdEnvAllowlist))
		for key := range cmdEnvAllowlist {
			if val, ok := env[key]; ok {
				filtered[key] = val
			}
		}
		return filtered
	}

	vars := ExtractEnvVars(name, cfg)
	if vars == nil {
		return env // extraction failed, keep original
	}

	filtered := make(map[string]string, len(vars))
	for key := range vars {
		if val, ok := env[key]; ok {
			filtered[key] = val
		}
	}
	return filtered
}

func walkNode(node parse.Node, visit func(*parse.CommandNode)) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *parse.ListNode:
		for _, child := range n.Nodes {
			walkNode(child, visit)
		}

	case *parse.ActionNode:
		walkNode(n.Pipe, visit)

	case *parse.PipeNode:
		for _, cmd := range n.Cmds {
			visit(cmd)
		}

	case *parse.IfNode:
		walkBranch(&n.BranchNode, visit)

	case *parse.RangeNode:
		walkBranch(&n.BranchNode, visit)

	case *parse.WithNode:
		walkBranch(&n.BranchNode, visit)

	case *parse.TemplateNode:
		if n.Pipe != nil {
			walkNode(n.Pipe, visit)
		}
	}
}

func walkBranch(b *parse.BranchNode, visit func(*parse.CommandNode)) {
	walkNode(b.Pipe, visit)
	walkNode(b.List, visit)
	walkNode(b.ElseList, visit)
}

// collectEnvVar extracts env var names from direct calls like {{ env "FOO" }}
// and {{ envDefault "BAR" "fallback" }}. It only detects calls where "env" or
// "envDefault" is the first identifier in the command. Piped expressions such
// as {{ "FOO" | env }} place "env" in a later pipeline stage, so the variable
// name won't be captured here. This is acceptable because FilterEnv falls back
// to passing all env vars when extraction returns nil or misses entries.
func collectEnvVar(cmd *parse.CommandNode, vars map[string]bool) {
	if len(cmd.Args) < 2 {
		return
	}

	ident, ok := cmd.Args[0].(*parse.IdentifierNode)
	if !ok {
		return
	}

	if ident.Ident != "env" && ident.Ident != "envDefault" {
		return
	}

	str, ok := cmd.Args[1].(*parse.StringNode)
	if !ok {
		return // dynamic var name, silently skip
	}

	vars[str.Text] = true
}
