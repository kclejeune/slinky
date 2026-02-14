package render

import (
	"os"
	"text/template"
	"text/template/parse"

	"github.com/kclejeune/slinky/internal/config"
)

// ExtractEnvVars parses the template referenced by cfg and walks its AST to
// find all statically-referenced environment variable names (via env "KEY" and
// envDefault "KEY" "fallback" calls). Returns the set of referenced var names,
// or nil on any error (parse failure, missing template, command mode) as a
// "keep all env" fallback.
func ExtractEnvVars(cfg *config.FileConfig) map[string]bool {
	if cfg.Render == "command" || cfg.Template == "" {
		return nil
	}

	tplPath := config.ExpandPath(cfg.Template)
	tplData, err := os.ReadFile(tplPath)
	if err != nil {
		return nil
	}

	funcMap, err := buildFuncMap(nil, nil)
	if err != nil {
		return nil
	}

	tmpl, err := template.New(cfg.Name).Funcs(funcMap).Parse(string(tplData))
	if err != nil {
		return nil
	}

	vars := make(map[string]bool)
	for _, t := range tmpl.Templates() {
		if t.Tree != nil && t.Root != nil {
			walkNode(t.Root, vars)
		}
	}
	return vars
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
func FilterEnv(cfg *config.FileConfig, env map[string]string) map[string]string {
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

	vars := ExtractEnvVars(cfg)
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

func walkNode(node parse.Node, vars map[string]bool) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			walkNode(child, vars)
		}

	case *parse.ActionNode:
		walkNode(n.Pipe, vars)

	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, cmd := range n.Cmds {
			walkCommand(cmd, vars)
		}

	case *parse.IfNode:
		walkBranch(&n.BranchNode, vars)

	case *parse.RangeNode:
		walkBranch(&n.BranchNode, vars)

	case *parse.WithNode:
		walkBranch(&n.BranchNode, vars)

	case *parse.TemplateNode:
		if n.Pipe != nil {
			walkNode(n.Pipe, vars)
		}
	}
}

func walkBranch(b *parse.BranchNode, vars map[string]bool) {
	walkNode(b.Pipe, vars)
	walkNode(b.List, vars)
	walkNode(b.ElseList, vars)
}

// walkCommand extracts env var names from direct calls like {{ env "FOO" }} and
// {{ envDefault "BAR" "fallback" }}. It only detects calls where "env" or
// "envDefault" is the first identifier in the command. Piped expressions such as
// {{ "FOO" | env }} place "env" in a later pipeline stage, so the variable name
// won't be captured here. This is acceptable because FilterEnv falls back to
// passing all env vars when extraction returns nil or misses entries.
func walkCommand(cmd *parse.CommandNode, vars map[string]bool) {
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
