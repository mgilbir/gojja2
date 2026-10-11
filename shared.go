// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// SharedState names the ways a render may leave something behind for the next
// one. The zero value names none, and is the default: a render is stateless.
//
// jinja2 is not. It hands a template the caller's own objects, so a template
// that appends to a list appends to the caller's list, a macro module keeps
// what one call changed for the next, and a method the template calls runs on
// the caller's object. Reusing one context, one module or one host object
// across requests then means one request's template can change what the next
// one sees -- or what another tenant's sees.
//
// gojja2 copies what a template could change the first time a render reaches
// it -- a render argument, an environment global, what a host function returns
// -- so the render works on its own copy and nothing it does outlives it. Where
// a copy is not possible it refuses instead, with an error wrapping
// [ErrSharedState] that names the flag allowing it. Each flag below gives back
// one of jinja2's behaviours, for templates trusted not to need the guarantee;
// the copies of globals, Render arguments and host results have no flag.
//
// What the guarantee cannot cover is code the engine cannot see into: a
// function, filter, test or [value.Object] the host registers or passes in,
// and a Go method declared on a value that writes through a map, a slice or a
// pointer the value holds. Those run as written, so whether they keep state is
// up to the host that wrote them.
type SharedState uint8

const (
	// ShareRenderValues hands [Template.RenderValues],
	// [Template.RenderBlockValues], [Expression.EvalValues] and
	// [Module.CallArgs] their values as they are, without the copy
	// [Template.Render] makes.
	// A template that changes one in place -- `{% do xs.append(9) %}` --
	// changes the caller's value, for every later render given it.
	ShareRenderValues SharedState = 1 << iota
	// ShareModuleState lets a [Module] keep what one call changed for the
	// next, as jinja2's TemplateModule does: a macro appending to a
	// module-level list sees the list the previous call left. Without it
	// every call starts from the state the module had when [Template.Module]
	// returned.
	ShareModuleState
	// AllowPointerMethods lets a template call a Go method declared on a
	// pointer receiver, which can change the object the host passed in.
	// Without it such a call is refused, and methods declared on the value
	// -- which receive a copy of the struct -- are called as usual.
	AllowPointerMethods

	// ShareAll is all three. It is not jinja2's behaviour throughout: an
	// environment global, a [Template.Render] argument and what a host
	// function returns are copied whatever the flags say, since a render
	// that changed those would change them for every render after it.
	ShareAll = ShareRenderValues | ShareModuleState | AllowPointerMethods
)

// ErrSharedState reports that a template tried to do something that could
// carry state from one render into another, which the environment's
// [SharedState] does not allow. See [WithSharedState].
var ErrSharedState = errors.New("gojja2: a render may not change state that outlives it")

// WithSharedState chooses which of jinja2's stateful behaviours an environment
// keeps. The default, the zero value, keeps none; see [SharedState]. A value
// with a bit that is not one of the flags is refused, so a typo cannot quietly
// widen what a template may do.
func WithSharedState(s SharedState) Option {
	return func(e *Environment) error {
		if s&^ShareAll != 0 {
			return fmt.Errorf("gojja2: unknown SharedState %#x", uint8(s))
		}
		e.shared = s
		return nil
	}
}

// pointerMethod is what value's bound Go method reports about its receiver.
type pointerMethod interface {
	PointerReceiver() (ptr bool, owner reflect.Type, name string)
}

// refuseSharedCall is the check a template's call makes before it runs: a Go
// method declared on a pointer can change the host's object, and so carry
// state into the next render, unless the environment allows it.
func (s *State) refuseSharedCall(callee value.Value) error {
	if s == nil || s.env.shared&AllowPointerMethods != 0 {
		return nil
	}
	m, ok := callee.Interface().(pointerMethod)
	if !ok {
		return nil
	}
	ptr, owner, name := m.PointerReceiver()
	if !ptr {
		return nil
	}
	e := errs.New(errs.TemplateRuntimeError,
		"%s.%s has a pointer receiver, so calling it could change the object the "+
			"host passed in; allow it with WithSharedState(AllowPointerMethods)",
		owner, name)
	e.Cause = ErrSharedState
	return e
}
