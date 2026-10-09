// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/parser"
)

// TemplateReference is one template named by an `{% extends %}`,
// `{% include %}`, `{% import %}` or `{% from %}` tag. See
// [Template.ReferencedTemplates].
type TemplateReference struct {
	// Name is the template named, when the tag writes it as a string
	// literal. It is the name as written, before any [WithJoinPath].
	Name string
	// Dynamic reports that the name is decided at render time -- a
	// variable, a concatenation, a conditional, a filter, a literal that
	// is not a string -- so nothing short of rendering knows it. Name is
	// empty then. It is the None jinja2 yields.
	Dynamic bool
	// Line is the line of the tag.
	Line int
}

// ReferencedTemplates lists the templates this one names, in the order the tags
// appear -- inside blocks, macros, loops and conditions as much as at the top --
// which is jinja2's meta.find_referenced_templates. It is meant for dependency
// tracking: what to rebuild when a layout changes.
//
// A name written as a string literal is listed by that name. A list or tuple of
// candidates lists each one: a string literal by name, any other literal not at
// all, and anything else as a [TemplateReference] with Dynamic set. Any other
// name expression is a single Dynamic reference, so the list says where a
// dependency exists even when it cannot say on what. A tag naming the same
// template twice is listed twice.
//
// Like [Template.Syntax] it reads the template as written rather than as
// compiled, so `{% include 'a' ~ 'b' %}` is Dynamic although the compiler folds
// it to "ab": jinja2 asks the parsed tree, before folding, too.
func (t *Template) ReferencedTemplates() []TemplateReference {
	tree, err := parser.Parse(t.env.syntax, t.env.parseOpts, t.source, t.name)
	if err != nil {
		return nil
	}
	refs := []TemplateReference{}
	ast.Inspect(tree, func(n ast.Node) bool {
		var name ast.Expr
		switch n := n.(type) {
		case *ast.Extends:
			name = n.Template
		case *ast.Include:
			name = n.Template
		case *ast.Import:
			name = n.Template
		case *ast.FromImport:
			name = n.Template
		default:
			return true
		}
		line := n.Line()
		var items []ast.Expr
		switch e := name.(type) {
		case *ast.Const:
			if e.Value.IsString() {
				refs = append(refs, TemplateReference{Name: e.Value.AsString(), Line: line})
			} else {
				refs = append(refs, TemplateReference{Dynamic: true, Line: line})
			}
			return false
		case *ast.List:
			items = e.Items
		case *ast.Tuple:
			items = e.Items
		default:
			refs = append(refs, TemplateReference{Dynamic: true, Line: line})
			return false
		}
		for _, item := range items {
			c, ok := item.(*ast.Const)
			switch {
			case !ok:
				refs = append(refs, TemplateReference{Dynamic: true, Line: line})
			case c.Value.IsString():
				refs = append(refs, TemplateReference{Name: c.Value.AsString(), Line: line})
			}
			// A literal that is not a string is skipped, as jinja2
			// skips it: "non-string consts that really just make no
			// sense".
		}
		return false
	})
	return refs
}
