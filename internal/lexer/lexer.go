// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Tokenize lexes a template.
//
// The scanner is a state machine over normalised source rather than the
// alternation of regexes jinja2 uses, but it reproduces the same decisions:
// which delimiter wins when two could match at one position, when whitespace
// either side of a tag is eaten, and when `%}` inside a tag is an end
// delimiter rather than a modulo followed by a brace.
func Tokenize(syn Syntax, source, name string) ([]Token, error) {
	l := &lexer{
		syn:          syn.withDefaults(),
		src:          normalizeNewlines(source, syn.KeepTrailingNewline),
		name:         name,
		line:         1,
		lineStarting: true,
	}
	// On failure the tokens lexed so far are still returned, with the error
	// alongside. jinja2's lexer is a generator the parser pulls from, so a
	// bad character late in a tag is only reported if the parser gets that
	// far -- `{{ 'a': b }}` fails on the colon there, not on the brace
	// after it. Returning the prefix lets the parser reproduce that order.
	err := l.run()
	l.emit(EOF, "")
	return l.out, err
}

// normalizeNewlines collapses \r\n and \r to \n so the rest of the lexer only
// has to think about one line terminator, and drops the template's final
// newline unless it was asked to keep it.
func normalizeNewlines(src string, keepTrailing bool) string {
	if strings.ContainsRune(src, '\r') {
		src = strings.ReplaceAll(src, "\r\n", "\n")
		src = strings.ReplaceAll(src, "\r", "\n")
	}
	if !keepTrailing {
		src = strings.TrimSuffix(src, "\n")
	}
	return src
}

type lexer struct {
	syn  Syntax
	src  string
	name string
	pos  int
	line int
	out  []Token

	// lineStarting records whether the previous match ended on a newline,
	// which decides whether lstrip_blocks applies to a tag in the first
	// line of the template.
	lineStarting bool
	// balance holds the closing brackets still owed inside a tag. While it
	// is non-empty an end delimiter is not recognised, so the `}` in
	// `{{ {"a": 1} }}` closes the dict rather than the print tag.
	balance []byte
	// lineStatement marks the tag currently being lexed as one opened by
	// the line-statement prefix, which ends at the newline rather than at
	// the block delimiter. findTag decides it; see isLineStatement.
	lineStatement bool

	// scan remembers where each opening delimiter was last found. See
	// delimScan: without it, a delimiter a template never uses costs a
	// scan of everything left on every tag.
	scan [scanKinds]delimScan
}

// The delimiters findTag looks for, one cursor each.
const (
	scanComment = iota
	scanBlock
	scanVariable
	scanLineStatement
	scanLineComment
	scanKinds
)

// delimScan remembers the next position of one delimiter.
//
// findTag is called once per tag and asks about every delimiter, and the
// cursor only ever moves forward. Searching afresh each time makes a delimiter
// the template never uses cost a scan to the end of the source *per tag*,
// which is quadratic in the number of tags: 800 KB of `{{1}}` took 8.7 seconds
// to compile and 1.6 MB took 34.5, while the same 5 MB with all three
// delimiters present took 0.48. Compilation has no budget and no context, so
// there was nothing to bound it either.
//
// Remembering the answer makes each delimiter's search pointer monotonic, so
// the whole lex is linear in the source however many tags it holds.
type delimScan struct {
	// at is the first occurrence at or after from, or -1 when there is
	// none left in the source.
	at   int
	from int
	// known distinguishes "searched, found nothing" from "never searched".
	known bool
}

// find returns the first occurrence of delim at or after from.
func (d *delimScan) find(src, delim string, from int) int {
	if d.known && d.from <= from {
		// Nothing at or after an earlier position means nothing at or
		// after this one either.
		if d.at < 0 {
			return -1
		}
		// The remembered hit is still the first one: there was nothing
		// between the earlier position and it, so there is nothing
		// between here and it.
		if d.at >= from {
			return d.at
		}
	}
	i := strings.Index(src[from:], delim)
	d.known, d.from = true, from
	if i < 0 {
		d.at = -1
	} else {
		d.at = from + i
	}
	return d.at
}

func (l *lexer) errorf(line int, format string, args ...any) error {
	e := errs.New(errs.TemplateSyntaxError, format, args...)
	e.Line = line
	e.Name = l.name
	e.Source = l.src
	return e
}

func (l *lexer) emit(k Kind, v string) {
	l.out = append(l.out, Token{Kind: k, Value: v, Line: l.line})
}

// advance moves past n bytes of source, keeping the line counter in step.
func (l *lexer) advance(n int) string {
	text := l.src[l.pos : l.pos+n]
	l.pos += n
	l.line += strings.Count(text, "\n")
	l.lineStarting = strings.HasSuffix(text, "\n")
	return text
}

// --- tag discovery -----------------------------------------------------------

type tagKind uint8

const (
	tagVariable tagKind = iota
	tagLineStatement
	tagLineComment
	tagComment
	tagBlock
	tagRaw
)

// tagMatch describes an opening delimiter found in template data.
type tagMatch struct {
	kind tagKind
	// start is where the delimiter's match begins. For a line statement or
	// line comment this includes the leading horizontal whitespace, which
	// is part of the delimiter rather than part of the data before it.
	start int
	// delimEnd is the first byte after the delimiter proper. It differs
	// from start + len(delimiter) for line statements and line comments,
	// whose match begins at the indentation in front of the prefix.
	delimEnd int
	// bodyStart is the first byte after the delimiter and its whitespace
	// control sign.
	bodyStart int
	// sign is '-', '+' or 0.
	sign byte
}

// candidatePriority ranks delimiters that could match at the same position.
//
// jinja2 sorts its alternation by delimiter length descending and then by
// token name descending, and regex alternation takes the first that matches.
// The order only becomes observable with custom delimiters that share a
// prefix, but it is cheap to be faithful about.
func candidatePriority(k tagKind) int {
	switch k {
	case tagVariable:
		return 4
	case tagLineStatement:
		return 3
	case tagLineComment:
		return 2
	case tagComment:
		return 1
	default: // tagBlock
		return 0
	}
}

// findTag locates the next opening delimiter at or after from.
func (l *lexer) findTag(from int) (tagMatch, bool) {
	best := tagMatch{start: -1}
	bestLen := 0

	consider := func(k tagKind, start, delimLen, delimEnd int) {
		if start < 0 {
			return
		}
		switch {
		case best.start < 0 || start < best.start:
		case start > best.start:
			return
		case delimLen > bestLen:
		case delimLen < bestLen:
			return
		case candidatePriority(k) <= candidatePriority(best.kind):
			return
		}
		best = tagMatch{kind: k, start: start, delimEnd: delimEnd}
		bestLen = delimLen
	}

	for _, c := range []struct {
		kind  tagKind
		slot  int
		delim string
	}{
		{tagComment, scanComment, l.syn.CommentStart},
		{tagBlock, scanBlock, l.syn.BlockStart},
		{tagVariable, scanVariable, l.syn.VariableStart},
	} {
		if at := l.scan[c.slot].find(l.src, c.delim, from); at >= 0 {
			consider(c.kind, at, len(c.delim), at+len(c.delim))
		}
	}
	if p := l.syn.LineStatementPrefix; p != "" {
		if start, prefixAt, ok := l.findLinePrefix(from, p, true, scanLineStatement); ok {
			consider(tagLineStatement, start, len(p), prefixAt+len(p))
		}
	}
	if p := l.syn.LineCommentPrefix; p != "" {
		if start, prefixAt, ok := l.findLinePrefix(from, p, false, scanLineComment); ok {
			consider(tagLineComment, start, len(p), prefixAt+len(p))
		}
	}

	if best.start < 0 {
		return tagMatch{}, false
	}

	// Resolve the whitespace-control sign and, for block tags, whether this
	// is really a raw block.
	after := best.delimEnd
	if best.kind == tagBlock || best.kind == tagComment || best.kind == tagVariable {
		if after < len(l.src) && (l.src[after] == '-' || l.src[after] == '+') {
			best.sign = l.src[after]
			after++
		}
	}
	best.bodyStart = after

	if best.kind == tagBlock {
		if end, ok := l.matchRawOpen(after); ok {
			best.kind = tagRaw
			best.bodyStart = end
		}
	}
	return best, true
}

// findLinePrefix finds a line-statement or line-comment prefix, returning the
// start of the match including the horizontal whitespace that precedes it.
//
// A line statement must begin a line; a line comment may also follow other
// content on the line, which is why the two differ only in that final check.
// The accepted candidate is remembered the same way the plain delimiters are,
// so a prefix that never matches is not re-scanned for at every tag. Only the
// prefix's own position is cached: the whitespace in front of it is measured
// back from the current cursor, which moves.
func (l *lexer) findLinePrefix(from int, prefix string, mustStartLine bool, slot int) (start, prefixAt int, ok bool) {
	for search := from; ; {
		at := l.scan[slot].find(l.src, prefix, search)
		if at < 0 {
			return 0, 0, false
		}
		start = at
		for start > from && isHorizontalSpace(l.src[start-1]) {
			start--
		}
		atLineStart := start == 0 || l.src[start-1] == '\n'
		if atLineStart || !mustStartLine {
			return start, at, true
		}
		search = at + len(prefix)
	}
}

// matchRawOpen reports whether `{%` at this point opens a raw block, returning
// the offset just past the tag. The pattern is `\s*raw\s*` followed by the
// block end, optionally sign-prefixed -- note that trim_blocks does not apply
// here, so `{% raw %}\n` keeps its newline.
func (l *lexer) matchRawOpen(pos int) (int, bool) {
	p := skipSpaceAt(l.src, pos)
	if !strings.HasPrefix(l.src[p:], "raw") {
		return 0, false
	}
	p = skipSpaceAt(l.src, p+len("raw"))
	if strings.HasPrefix(l.src[p:], "-"+l.syn.BlockEnd) {
		return skipSpaceAt(l.src, p+1+len(l.syn.BlockEnd)), true
	}
	if strings.HasPrefix(l.src[p:], l.syn.BlockEnd) {
		return p + len(l.syn.BlockEnd), true
	}
	return 0, false
}

// --- the root loop -----------------------------------------------------------

func (l *lexer) run() error {
	for l.pos < len(l.src) {
		tag, ok := l.findTag(l.pos)
		if !ok {
			l.emitData(l.src[l.pos:])
			l.advance(len(l.src) - l.pos)
			return nil
		}

		l.emitData(l.trimBeforeTag(l.src[l.pos:tag.start], tag))
		l.advance(tag.start - l.pos)
		// The delimiter's own text becomes the token's value, sign and
		// line-statement indentation included, so a token stream can be
		// spliced back into source.
		delim := l.advance(tag.bodyStart - l.pos)

		var err error
		switch tag.kind {
		case tagComment:
			err = l.lexComment()
		case tagLineComment:
			err = l.lexLineComment()
		case tagRaw:
			err = l.lexRaw()
		case tagBlock:
			l.emit(BlockBegin, delim)
			err = l.lexTag(BlockEnd)
		case tagVariable:
			l.emit(VariableBegin, delim)
			err = l.lexTag(VariableEnd)
		case tagLineStatement:
			l.emit(BlockBegin, delim)
			l.lineStatement = true
			err = l.lexTag(BlockEnd)
			l.lineStatement = false
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// emitData emits a data token, dropping it when empty -- jinja2 does not
// produce zero-length data tokens, and the parser would trip over them.
func (l *lexer) emitData(text string) {
	if text == "" {
		return
	}
	if l.syn.NewlineSequence != "\n" {
		text = strings.ReplaceAll(text, "\n", l.syn.NewlineSequence)
	}
	l.emit(Data, text)
}

// trimBeforeTag applies the opening tag's whitespace control to the data that
// precedes it.
func (l *lexer) trimBeforeTag(text string, tag tagMatch) string {
	switch {
	case tag.sign == '-':
		// `{%-` eats every kind of whitespace back to the last content.
		return strings.TrimRightFunc(text, isPySpaceRune)
	case tag.sign == '+':
		// `{%+` opts out of lstrip_blocks explicitly.
		return text
	case !l.syn.LstripBlocks || tag.kind == tagVariable:
		return text
	}
	// lstrip_blocks: drop the indentation in front of a block tag, but only
	// if nothing else shares the line.
	lineStart := strings.LastIndexByte(text, '\n') + 1
	if lineStart == 0 && !l.lineStarting {
		return text
	}
	rest := text[lineStart:]
	if rest == "" || !isAllPySpace(rest) {
		return text
	}
	return text[:lineStart]
}

// --- tag bodies --------------------------------------------------------------

// lexComment consumes a comment body and its end delimiter. Comments produce
// no tokens at all.
func (l *lexer) lexComment() error {
	startLine := l.line
	i := strings.Index(l.src[l.pos:], l.syn.CommentEnd)
	if i < 0 {
		return l.errorf(startLine, "Missing end of comment tag")
	}
	end := l.pos + i
	// The sign, if any, sits immediately before the delimiter.
	signAt := end
	if signAt > l.pos && (l.src[signAt-1] == '-' || l.src[signAt-1] == '+') {
		signAt--
	}
	l.advance(end + len(l.syn.CommentEnd) - l.pos)
	l.consumeAfterEnd(signByte(l.src, signAt, end), true)
	return nil
}

// lexLineComment consumes a line comment, which ends at the newline and does
// not consume it.
func (l *lexer) lexLineComment() error {
	i := strings.IndexByte(l.src[l.pos:], '\n')
	if i < 0 {
		i = len(l.src) - l.pos
	}
	l.advance(i)
	return nil
}

// lexRaw consumes a raw block verbatim up to its endraw tag.
func (l *lexer) lexRaw() error {
	startLine := l.line
	// A raw tag with nothing after it at all is not an error in jinja2.
	// Its lexer looks for the body with a regex that requires one, so an
	// empty remainder never reaches the "missing end" branch and the
	// tokenizer simply stops -- `{% raw %}` renders "" while
	// `{% raw %}abc` raises. That is an accident of the regex rather than
	// a rule, but it is the specification, and the two differ only on a
	// template that is already broken.
	if l.pos >= len(l.src) {
		return nil
	}
	search := l.pos
	for {
		i := strings.Index(l.src[search:], l.syn.BlockStart)
		if i < 0 {
			return l.errorf(startLine, "Missing end of raw directive")
		}
		at := search + i
		p := at + len(l.syn.BlockStart)
		var sign byte
		if p < len(l.src) && (l.src[p] == '-' || l.src[p] == '+') {
			sign = l.src[p]
			p++
		}
		q := skipSpaceAt(l.src, p)
		if !strings.HasPrefix(l.src[q:], "endraw") {
			search = at + len(l.syn.BlockStart)
			continue
		}
		q = skipSpaceAt(l.src, q+len("endraw"))

		var endSign byte
		if q < len(l.src) && (l.src[q] == '-' || l.src[q] == '+') {
			endSign = l.src[q]
			q++
		}
		if !strings.HasPrefix(l.src[q:], l.syn.BlockEnd) {
			search = at + len(l.syn.BlockStart)
			continue
		}

		body := l.src[l.pos:at]
		if sign == '-' {
			body = strings.TrimRightFunc(body, isPySpaceRune)
		}
		l.emitData(body)
		l.advance(q + len(l.syn.BlockEnd) - l.pos)
		l.consumeAfterEnd(endSign, true)
		return nil
	}
}

// lexTag lexes the inside of a `{% %}` or `{{ }}` tag up to its end delimiter.
func (l *lexer) lexTag(end Kind) error {
	for {
		if len(l.balance) == 0 {
			if done, err := l.tryEnd(end); done || err != nil {
				return err
			}
		}
		if l.pos >= len(l.src) {
			// jinja2's lexer simply stops here and lets the parser
			// report the unterminated tag as an unexpected EOF.
			return nil
		}
		if n := spaceRunAt(l.src, l.pos); n > 0 {
			l.advance(n)
			continue
		}
		if err := l.lexExprToken(); err != nil {
			return err
		}
	}
}

// tryEnd consumes the tag's end delimiter if it is at the current position.
func (l *lexer) tryEnd(end Kind) (bool, error) {
	if end == BlockEnd && l.isLineStatement() {
		return l.tryLineStatementEnd()
	}
	closing := l.syn.VariableEnd
	if end == BlockEnd {
		closing = l.syn.BlockEnd
	}
	rest := l.src[l.pos:]
	line := l.line

	var text string
	switch {
	// `+%}` and `+#}` suppress trim_blocks. There is no `+}}`: jinja2 does
	// not trim after a print tag in the first place.
	case end == BlockEnd && strings.HasPrefix(rest, "+"+closing):
		text = l.advance(1 + len(closing))
	case strings.HasPrefix(rest, "-"+closing):
		text = l.advance(1+len(closing)) + l.consumeAfterEnd('-', end == BlockEnd)
	case strings.HasPrefix(rest, closing):
		text = l.advance(len(closing)) + l.consumeAfterEnd(0, end == BlockEnd)
	default:
		return false, nil
	}
	l.out = append(l.out, Token{Kind: end, Value: text, Line: line})
	return true, nil
}

// isLineStatement reports whether the tag currently open was introduced by a
// line statement prefix rather than by `{%`.
// isLineStatement reports whether the tag being lexed is a line statement,
// which ends at its newline rather than at the block delimiter.
//
// findTag has already decided this, so the answer is carried from there. It
// used to be read back off the emitted token -- "the opening token does not
// begin with the block delimiter" -- which is true of every line statement
// until the prefix *is* the block delimiter. Configure both as "%" and every
// line statement was lexed as a `{% %}` tag hunting for a closing "%", which
// it found on the next line or not at all: jinja2 renders those templates.
func (l *lexer) isLineStatement() bool { return l.lineStatement }

// tryLineStatementEnd ends a line statement at the end of its line.
//
// jinja2 spells this `\s*(\n|$)`, whose backtracking consumes every trailing
// whitespace run up to and including its last newline -- so a blank line after
// a line statement is absorbed too.
func (l *lexer) tryLineStatementEnd() (bool, error) {
	n := spaceRunAt(l.src, l.pos)
	run := l.src[l.pos : l.pos+n]
	switch {
	case l.pos+n == len(l.src):
		// The run reaches the end of the template; `$` matches.
	case strings.ContainsRune(run, '\n'):
		n = strings.LastIndexByte(run, '\n') + 1
	default:
		return false, nil
	}
	line := l.line
	text := l.advance(n)
	l.out = append(l.out, Token{Kind: BlockEnd, Value: text, Line: line})
	return true, nil
}

// consumeAfterEnd applies the closing tag's trailing whitespace policy:
// `-%}` eats all following whitespace, and trim_blocks eats one newline.
func (l *lexer) consumeAfterEnd(sign byte, isBlockLike bool) string {
	switch {
	case sign == '-':
		return l.advance(spaceRunAt(l.src, l.pos))
	case sign == '+':
		// Explicitly opted out of trim_blocks.
	case isBlockLike && l.syn.TrimBlocks:
		if l.pos < len(l.src) && l.src[l.pos] == '\n' {
			return l.advance(1)
		}
	}
	return ""
}

// signByte returns the whitespace-control sign sitting at from, if the span
// [from, to) is one.
func signByte(src string, from, to int) byte {
	if to-from == 1 && (src[from] == '-' || src[from] == '+') {
		return src[from]
	}
	return 0
}

// --- expression tokens -------------------------------------------------------

func (l *lexer) lexExprToken() error {
	if n := l.matchFloat(); n > 0 {
		l.emit(Float, l.advance(n))
		return nil
	}
	if n := l.matchInteger(); n > 0 {
		l.emit(Integer, l.advance(n))
		return nil
	}
	if n := l.matchName(); n > 0 {
		l.emit(Name, l.advance(n))
		return nil
	}
	if n, ok := l.matchString(); ok {
		line := l.line
		raw := l.advance(n)
		text, err := decodeStringLiteral(raw[1:len(raw)-1], l.syn.NewlineSequence)
		if err != nil {
			return l.errorf(line, "%s", err.Error())
		}
		l.out = append(l.out, Token{Kind: String, Value: text, Line: line})
		return nil
	}
	if kind, n, ok := l.matchOperator(); ok {
		if err := l.trackBalance(kind); err != nil {
			return err
		}
		l.advance(n)
		l.emit(kind, symbols[kind])
		return nil
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
	return l.errorf(l.line, "unexpected char %s at %d", value.Repr(value.String(string(r))), l.pos)
}

// trackBalance maintains the bracket stack that decides whether an end
// delimiter is really an end delimiter.
func (l *lexer) trackBalance(kind Kind) error {
	switch kind {
	case LBrace:
		l.balance = append(l.balance, '}')
	case LParen:
		l.balance = append(l.balance, ')')
	case LBracket:
		l.balance = append(l.balance, ']')
	case RBrace, RParen, RBracket:
		got, _ := kind.Symbol()
		if len(l.balance) == 0 {
			return l.errorf(l.line, "unexpected '%s'", got)
		}
		want := l.balance[len(l.balance)-1]
		l.balance = l.balance[:len(l.balance)-1]
		if string(want) != got {
			return l.errorf(l.line, "unexpected '%s', expected '%c'", got, want)
		}
	}
	return nil
}

func (l *lexer) matchOperator() (Kind, int, bool) {
	rest := l.src[l.pos:]
	for _, op := range operatorsByLength {
		if strings.HasPrefix(rest, op.text) {
			return op.kind, len(op.text), true
		}
	}
	return EOF, 0, false
}

// matchName scans a Python identifier.
func (l *lexer) matchName() int {
	i := 0
	for i < len(l.src[l.pos:]) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos+i:])
		if i == 0 {
			if !isIdentStart(r) {
				return 0
			}
		} else if !isIdentContinue(r) {
			break
		}
		i += size
	}
	return i
}

// matchString scans a quoted string literal, which may span lines.
func (l *lexer) matchString() (int, bool) {
	rest := l.src[l.pos:]
	if rest == "" {
		return 0, false
	}
	quote := rest[0]
	if quote != '\'' && quote != '"' {
		return 0, false
	}
	for i := 1; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			i++ // the escaped byte is consumed whatever it is
		case quote:
			return i + 1, true
		}
	}
	return 0, false
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.Is(unicode.Nl, r)
}

func isIdentContinue(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r) ||
		unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r)
}

// --- whitespace helpers ------------------------------------------------------

func isHorizontalSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\v'
}

// isPySpaceRune matches what Python's str.isspace does, which is a slightly
// wider set than Go's unicode.IsSpace: the C1 file/group/record separators
// count as whitespace there.
func isPySpaceRune(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}

func isAllPySpace(s string) bool {
	for _, r := range s {
		if !isPySpaceRune(r) {
			return false
		}
	}
	return true
}

// spaceRunAt returns the length in bytes of the whitespace run at pos.
func spaceRunAt(s string, pos int) int {
	i := pos
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isPySpaceRune(r) {
			break
		}
		i += size
	}
	return i - pos
}

func skipSpaceAt(s string, pos int) int { return pos + spaceRunAt(s, pos) }
