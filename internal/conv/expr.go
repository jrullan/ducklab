// Package conv implements the conversation engine: the deterministic turn
// scheduler, the transcript, anonymisation, and the Until expression language.
//
// Turn order is data, never a model's choice. That is the property the whole
// design rests on, so nothing in this package may consult a model.
package conv

import (
	"fmt"
	"strconv"
	"strings"
)

// The Until grammar (05 §3.3), exactly:
//
//	expr := term (("and" | "or") term)*
//	term := "not"? atom
//	atom := IDENT (("==" | "!=") LITERAL)? | "(" expr ")"
//
// There is no arbitrary evaluation: only the identifiers in the table below,
// two comparison operators, and parentheses. `os.Exit(1)` is a lexer error,
// not a call.
//
// One deliberate strengthening: the grammar puts `and` and `or` at the same
// precedence, so `a or b and c` would parse as `(a or b) and c` — an order
// almost nobody expects. Rather than invent a precedence the spec does not
// define, mixing them without parentheses is rejected at load time. A script
// author gets an error instead of a silently surprising loop condition.

// Kind is the type of an Until identifier.
type Kind int

const (
	KindString Kind = iota
	KindBool
	KindInt
)

// identKinds is the closed set of identifiers an Until expression may use.
// Anything else is a load-time error (05 §3.3).
var identKinds = map[string]Kind{
	"gate":        KindString, // green | red | none
	"verdict":     KindString, // approve | request-changes | ""
	"choice":      KindString, // A | B | … | none | ""
	"changed":     KindBool,
	"no_findings": KindBool,
	"round":       KindInt,
}

// State is what an Until expression is evaluated against.
type State struct {
	Gate       string
	Verdict    string
	Choice     string
	Changed    bool
	NoFindings bool
	Round      int
}

func (s *State) lookup(ident string) (value, error) {
	switch ident {
	case "gate":
		return value{kind: KindString, str: s.Gate}, nil
	case "verdict":
		return value{kind: KindString, str: s.Verdict}, nil
	case "choice":
		return value{kind: KindString, str: s.Choice}, nil
	case "changed":
		return value{kind: KindBool, boolean: s.Changed}, nil
	case "no_findings":
		return value{kind: KindBool, boolean: s.NoFindings}, nil
	case "round":
		return value{kind: KindInt, integer: s.Round}, nil
	}
	return value{}, fmt.Errorf("unknown identifier %q", ident)
}

type value struct {
	kind    Kind
	str     string
	boolean bool
	integer int
}

// truthy defines a bare identifier's meaning: a bool is itself, a string is
// non-empty, an int is non-zero.
func (v value) truthy() bool {
	switch v.kind {
	case KindBool:
		return v.boolean
	case KindString:
		return v.str != ""
	case KindInt:
		return v.integer != 0
	}
	return false
}

func (v value) equals(o value) bool {
	if v.kind != o.kind {
		return false
	}
	switch v.kind {
	case KindString:
		return v.str == o.str
	case KindBool:
		return v.boolean == o.boolean
	case KindInt:
		return v.integer == o.integer
	}
	return false
}

// --- lexer ------------------------------------------------------------------

type tokenType int

const (
	tokIdent tokenType = iota
	tokString
	tokInt
	tokBool
	tokEq
	tokNeq
	tokAnd
	tokOr
	tokNot
	tokLParen
	tokRParen
	tokEOF
)

type token struct {
	typ tokenType
	str string
	num int
	pos int
}

func lex(input string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(input) {
		c := input[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{typ: tokLParen, pos: i})
			i++
		case c == ')':
			toks = append(toks, token{typ: tokRParen, pos: i})
			i++
		case c == '=' || c == '!':
			if i+1 >= len(input) || input[i+1] != '=' {
				return nil, fmt.Errorf("at %d: expected %c=", i, c)
			}
			t := tokEq
			if c == '!' {
				t = tokNeq
			}
			toks = append(toks, token{typ: t, pos: i})
			i += 2
		case c == '"':
			j := i + 1
			for j < len(input) && input[j] != '"' {
				j++
			}
			if j >= len(input) {
				return nil, fmt.Errorf("at %d: unterminated string", i)
			}
			toks = append(toks, token{typ: tokString, str: input[i+1 : j], pos: i})
			i = j + 1
		case c >= '0' && c <= '9':
			j := i
			for j < len(input) && input[j] >= '0' && input[j] <= '9' {
				j++
			}
			n, _ := strconv.Atoi(input[i:j])
			toks = append(toks, token{typ: tokInt, num: n, pos: i})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(input) && isIdentChar(input[j]) {
				j++
			}
			word := input[i:j]
			switch word {
			case "and":
				toks = append(toks, token{typ: tokAnd, pos: i})
			case "or":
				toks = append(toks, token{typ: tokOr, pos: i})
			case "not":
				toks = append(toks, token{typ: tokNot, pos: i})
			case "true", "false":
				toks = append(toks, token{typ: tokBool, str: word, pos: i})
			default:
				toks = append(toks, token{typ: tokIdent, str: word, pos: i})
			}
			i = j
		default:
			return nil, fmt.Errorf("at %d: unexpected character %q", i, string(c))
		}
	}
	toks = append(toks, token{typ: tokEOF, pos: len(input)})
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// --- AST --------------------------------------------------------------------

type node interface {
	eval(*State) (bool, error)
}

type binaryNode struct {
	op          tokenType // tokAnd | tokOr
	left, right node
}

func (n *binaryNode) eval(s *State) (bool, error) {
	l, err := n.left.eval(s)
	if err != nil {
		return false, err
	}
	// Short-circuit, so a later term cannot mask an earlier decision.
	if n.op == tokAnd && !l {
		return false, nil
	}
	if n.op == tokOr && l {
		return true, nil
	}
	return n.right.eval(s)
}

type notNode struct{ inner node }

func (n *notNode) eval(s *State) (bool, error) {
	v, err := n.inner.eval(s)
	return !v, err
}

type identNode struct{ name string }

func (n *identNode) eval(s *State) (bool, error) {
	v, err := s.lookup(n.name)
	if err != nil {
		return false, err
	}
	return v.truthy(), nil
}

type compareNode struct {
	ident string
	neq   bool
	lit   value
}

func (n *compareNode) eval(s *State) (bool, error) {
	v, err := s.lookup(n.ident)
	if err != nil {
		return false, err
	}
	eq := v.equals(n.lit)
	if n.neq {
		return !eq, nil
	}
	return eq, nil
}

// --- parser -----------------------------------------------------------------

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) atEOF() bool { return p.peek().typ == tokEOF }

func (p *parser) parseExpr() (node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	seen := map[tokenType]bool{}
	for p.peek().typ == tokAnd || p.peek().typ == tokOr {
		op := p.next()
		seen[op.typ] = true
		if seen[tokAnd] && seen[tokOr] {
			return nil, fmt.Errorf("at %d: mixing 'and' with 'or' needs parentheses; "+
				"the grammar gives them equal precedence, so the intended grouping is ambiguous", op.pos)
		}
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: op.typ, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseTerm() (node, error) {
	if p.peek().typ == tokNot {
		p.next()
		inner, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		return &notNode{inner: inner}, nil
	}
	return p.parseAtom()
}

func (p *parser) parseAtom() (node, error) {
	t := p.next()
	switch t.typ {
	case tokLParen:
		// A nested expression gets its own and/or scope.
		inner, err := (&parser{toks: p.toks, pos: p.pos}).parseParenthesised(p)
		if err != nil {
			return nil, err
		}
		return inner, nil
	case tokIdent:
		kind, ok := identKinds[t.str]
		if !ok {
			return nil, fmt.Errorf("at %d: unknown identifier %q (allowed: %s)",
				t.pos, t.str, strings.Join(KnownIdentifiers(), ", "))
		}
		if p.peek().typ != tokEq && p.peek().typ != tokNeq {
			return &identNode{name: t.str}, nil
		}
		op := p.next()
		lit, err := p.parseLiteral(kind, t.str)
		if err != nil {
			return nil, err
		}
		return &compareNode{ident: t.str, neq: op.typ == tokNeq, lit: lit}, nil
	default:
		return nil, fmt.Errorf("at %d: expected an identifier or '('", t.pos)
	}
}

// parseParenthesised parses a sub-expression and syncs the outer parser's
// position past the closing paren.
func (p *parser) parseParenthesised(outer *parser) (node, error) {
	inner, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().typ != tokRParen {
		return nil, fmt.Errorf("at %d: expected ')'", p.peek().pos)
	}
	p.next()
	outer.pos = p.pos
	return inner, nil
}

func (p *parser) parseLiteral(kind Kind, ident string) (value, error) {
	t := p.next()
	switch t.typ {
	case tokString:
		if kind != KindString {
			return value{}, fmt.Errorf("at %d: %q is not a string identifier", t.pos, ident)
		}
		return value{kind: KindString, str: t.str}, nil
	case tokInt:
		if kind != KindInt {
			return value{}, fmt.Errorf("at %d: %q is not a numeric identifier", t.pos, ident)
		}
		return value{kind: KindInt, integer: t.num}, nil
	case tokBool:
		if kind != KindBool {
			return value{}, fmt.Errorf("at %d: %q is not a boolean identifier", t.pos, ident)
		}
		return value{kind: KindBool, boolean: t.str == "true"}, nil
	default:
		return value{}, fmt.Errorf("at %d: expected a literal after the comparison", t.pos)
	}
}

// --- public API -------------------------------------------------------------

// Expr is a compiled Until expression.
type Expr struct {
	src  string
	root node
}

// String returns the source text.
func (e *Expr) String() string { return e.src }

// Compile parses an Until expression. An empty expression is legal and never
// terminates the loop early — the round cap alone bounds it.
//
// Compile is called at script-load time so a bad expression fails before any
// model is invoked (I3, 05 §3.3).
func Compile(src string) (*Expr, error) {
	if strings.TrimSpace(src) == "" {
		return &Expr{src: src}, nil
	}
	toks, err := lex(src)
	if err != nil {
		return nil, fmt.Errorf("until %q: %w", src, err)
	}
	p := &parser{toks: toks}
	root, err := p.parseExpr()
	if err != nil {
		return nil, fmt.Errorf("until %q: %w", src, err)
	}
	if !p.atEOF() {
		return nil, fmt.Errorf("until %q: at %d: unexpected trailing input", src, p.peek().pos)
	}
	return &Expr{src: src, root: root}, nil
}

// Eval evaluates the expression against a state. An empty expression is false:
// the loop runs to its round cap.
func (e *Expr) Eval(s *State) (bool, error) {
	if e == nil || e.root == nil {
		return false, nil
	}
	return e.root.eval(s)
}

// KnownIdentifiers lists the identifiers an Until expression may use, sorted.
func KnownIdentifiers() []string {
	out := make([]string, 0, len(identKinds))
	for name := range identKinds {
		out = append(out, name)
	}
	// Small fixed set; insertion sort keeps this dependency-free.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
