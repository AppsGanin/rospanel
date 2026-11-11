package sub

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"text/template/parse"

	"golang.org/x/net/html"
)

// A theme plugin can replace parts of the subscription page's markup, not only
// restyle it: theme/<section>.html is a template that renders in that section's
// place, with the same data. The page's dialogs and script stay the panel's, so a
// theme cannot run code: its markup has no script, no event handlers and no
// forms, and its buttons say what they do in data-action (handled by the page's
// script). What a theme template may use is checked against the page's real data
// when the plugin is installed — a misspelt field is an install error, not a blank
// spot — and nothing in it can loop longer than the page's own lists.
//
// Every section renders its "core:<section>" original unless the theme replaces
// it, and a theme template can call any original or section, so a theme that only
// wraps the QR card in its own frame is three lines.

// ThemeSections are the page's replaceable sections, layout first: layout puts the
// others in order, each in its pane.
var ThemeSections = append(append([]string{"layout"}, layoutSections...), append([]string{"dialogs"}, dialogSections...)...)

// layoutSections are what core:layout calls, in its order; dialogSections what
// core:dialogs does. The page itself calls layout and dialogs.
var (
	layoutSections = []string{
		"header", "badges", "notices", "tabs",
		"churn", "blocks", "wizard", "connect", "devices", "awg", "wg", "turn", "configs", "tgproxy", "access", "rotate",
		"plan", "promo", "wallet", "history", "referral", "invitees", "legal",
	}
	dialogSections = []string{"dialog-apps", "dialog-wizard", "dialog-renew", "dialog-change", "dialog-plans", "dialog-pay", "dialog-msg"}
)

// ThemeActions are what a theme's data-action may name (data-value carries the
// argument where one is taken). The page's script does the rest.
var ThemeActions = []string{
	"copy", "copy-icon", "copy-quiet", "apps", "app", "close", "wizard", "plans", "renew", "change", "tab", "rotate",
	"unbind-device", "churn", "churn-send", "cancel", "promo", "topup", "buy-extra", "external",
	"tg-proxy", "tg-access", "auto-renew", "addn",
	"wz-plat", "wz-go", "pick-plan", "period", "devices", "buy-plan", "change-plan", "pay-with",
}

// ThemeMarkup is the active theme's markup, as the page renders it.
type ThemeMarkup struct {
	Key     string            // plugin and package hash: the compiled page is cached by it
	Files   map[string][]byte // the theme's files by name: its templates and the assets they name
	Base    string            // where the theme's files are served, ending in "/"
	Version string            // the package hash, for the assets' cache-busting query
}

const (
	themeMaxOut = 4 << 20 // a rendered page past this is not a page
	themeMark   = "rpthemeslot"
)

// IsThemeTemplate says whether a theme file is a template rather than an asset.
func IsThemeTemplate(name string) bool { return strings.HasSuffix(name, ".html") }

// Asset is a theme file's address, for a theme template: {{$.Asset "bg.webp"}}.
// "" for a name the theme does not have.
func (d pageData) Asset(name string) string {
	m := d.Access.Markup
	if m == nil || IsThemeTemplate(name) {
		return ""
	}
	if _, ok := m.Files[name]; !ok {
		return ""
	}
	return m.Base + url.PathEscape(name) + "?v=" + url.QueryEscape(m.Version)
}

var (
	themeMu    sync.Mutex
	themeKey   string
	themeTmpl  *template.Template
	themeError error
)

// themed is the page template with m's sections in it, compiled once per package.
func themed(m *ThemeMarkup) (*template.Template, error) {
	themeMu.Lock()
	defer themeMu.Unlock()
	if themeKey == m.Key && (themeTmpl != nil || themeError != nil) {
		return themeTmpl, themeError
	}
	themeKey = m.Key
	themeTmpl, themeError = compileTheme(m.Files)
	return themeTmpl, themeError
}

func compileTheme(files map[string][]byte) (*template.Template, error) {
	if problems := checkTemplates(files); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	t, err := pageBase.Clone()
	if err != nil {
		return nil, err
	}
	for name, src := range files {
		if IsThemeTemplate(name) {
			// html/template keeps a section whose replacement is only blanks and
			// comments; an empty file means "show nothing here", so it gets a body.
			body := string(src) + `{{""}}`
			if _, err := t.New(strings.TrimSuffix(name, ".html")).Parse(body); err != nil {
				return nil, err
			}
		}
	}
	return t, nil
}

// renderThemed renders data with its theme; ok is false when there is none, or it
// failed — the page then renders as the panel's own.
func renderThemed(data pageData) ([]byte, bool) {
	m := data.Access.Markup
	if m == nil || !slices.ContainsFunc(themeNames(m.Files), IsThemeTemplate) {
		return nil, false
	}
	t, err := themed(m)
	if err == nil {
		w := &cappedWriter{max: themeMaxOut}
		if err = t.Execute(w, data); err == nil {
			return w.buf.Bytes(), true
		}
	}
	// The reader gets the panel's page; the operator learns why, once per package
	// rather than once per page view.
	if _, seen := themeFailed.LoadOrStore(m.Key, true); !seen {
		slog.Warn("theme not rendered: the subscription page shows as the panel's own", "theme", m.Key, "err", err)
	}
	return nil, false
}

// themeFailed holds the theme packages already reported as failing to render.
var themeFailed sync.Map

type cappedWriter struct {
	buf bytes.Buffer
	max int
}

var errTooLong = errors.New("the page is too long")

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.max {
		return 0, errTooLong
	}
	return w.buf.Write(p)
}

func themeNames(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CheckTheme lists what keeps a theme's templates from rendering: a file that is
// not a section, a field the page does not have, markup that could run code or
// load from elsewhere, a section that calls itself. Each line names its file.
func CheckTheme(files map[string][]byte) []string {
	problems := checkTemplates(files)
	if len(problems) > 0 {
		return problems
	}
	if !slices.ContainsFunc(themeNames(files), IsThemeTemplate) {
		return nil
	}
	// A dry run on an empty page: what the checks above cannot see (a template
	// ending inside a tag, a nil the page does have) shows up here, at install.
	t, err := compileTheme(files)
	if err == nil {
		data := pageData{Access: Access{Markup: &ThemeMarkup{Files: files}}}
		err = t.Execute(io.Discard, data)
	}
	if err != nil {
		return []string{"theme: " + err.Error()}
	}
	return nil
}

func checkTemplates(files map[string][]byte) []string {
	var problems []string
	calls := map[string][]string{}
	for _, name := range themeNames(files) {
		if !IsThemeTemplate(name) {
			continue
		}
		section := strings.TrimSuffix(name, ".html")
		at := "theme/" + name
		if !slices.Contains(ThemeSections, section) {
			problems = append(problems, fmt.Sprintf("%s: not a section of the page (%s)", at, strings.Join(ThemeSections, ", ")))
			continue
		}
		c := &checker{root: reflect.TypeOf(pageData{}), files: files}
		c.check(section, string(files[name]))
		for _, e := range c.errs {
			problems = append(problems, at+": "+e)
		}
		calls[section] = c.calls
	}
	if len(problems) == 0 {
		if cyc := cycle(calls); cyc != "" {
			problems = append(problems, "theme: sections call each other in a circle: "+cyc)
		}
	}
	return problems
}

// cycle finds a section that ends up calling itself, as "a → b → a"; "" if none.
// The page's own sections call nothing but layout, which calls them all.
func cycle(theme map[string][]string) string {
	edges := func(n string) []string {
		if c, ok := theme[n]; ok {
			return c
		}
		if n == "layout" {
			return []string{"core:layout"}
		}
		if n == "core:layout" {
			return layoutSections
		}
		if n == "core:dialogs" {
			return dialogSections
		}
		if !strings.HasPrefix(n, "core:") {
			return []string{"core:" + n}
		}
		return nil
	}
	state := map[string]int{} // 1 on the path, 2 done
	var path []string
	var visit func(n string) string
	visit = func(n string) string {
		switch state[n] {
		case 1:
			i := slices.Index(path, n)
			return strings.Join(append(slices.Clone(path[i:]), n), " → ")
		case 2:
			return ""
		}
		state[n] = 1
		path = append(path, n)
		for _, m := range edges(n) {
			if c := visit(m); c != "" {
				return c
			}
		}
		path = path[:len(path)-1]
		state[n] = 2
		return ""
	}
	names := make([]string, 0, len(theme))
	for n := range theme {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if c := visit(n); c != "" {
			return c
		}
	}
	return ""
}

// themeFuncs are the functions a theme template may call: the template language's
// own, less call (the page has no functions to call).
var themeFuncs = map[string]any{
	"and": true, "or": true, "not": true, "len": true, "index": true, "slice": true,
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
	"print": true, "printf": true, "println": true, "html": true, "js": true, "urlquery": true,
}

// checker walks one theme template with the type of what it is handed: the page's
// data at the top, an element inside a range. A field the type does not have is an
// error, and a range must be over one of the page's lists — a number would loop as
// many times as it says.
type checker struct {
	root   reflect.Type
	files  map[string][]byte
	scopes []map[string]reflect.Type
	calls  []string
	errs   []string
	ranges int // how many ranges the node at hand is inside
}

// themeMaxRanges is how deep ranges nest in a theme template. The page's lists are
// tens long (hundreds of links at most); a third level would loop a million times on
// every page view.
const themeMaxRanges = 2

func (c *checker) errf(format string, args ...any) {
	if len(c.errs) < 20 {
		c.errs = append(c.errs, fmt.Sprintf(format, args...))
	}
}

func (c *checker) check(name, src string) {
	trees, err := parse.Parse(name, src, "", "", themeFuncs)
	if err != nil {
		c.errf("%v", err)
		return
	}
	if len(trees) != 1 || trees[name] == nil {
		c.errf("a section is one template: no {{define}} or {{block}}")
		return
	}
	root := trees[name].Root
	c.scopes = []map[string]reflect.Type{{"$": c.root}}
	c.list(root, c.root)
	var b strings.Builder
	flatten(root, &b)
	for _, e := range checkMarkup(b.String()) {
		c.errf("%s", e)
	}
}

func (c *checker) push() { c.scopes = append(c.scopes, map[string]reflect.Type{}) }
func (c *checker) pop()  { c.scopes = c.scopes[:len(c.scopes)-1] }

func (c *checker) lookup(v string) (reflect.Type, bool) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if t, ok := c.scopes[i][v]; ok {
			return t, true
		}
	}
	return nil, false
}

func (c *checker) list(l *parse.ListNode, dot reflect.Type) {
	if l == nil {
		return
	}
	for _, n := range l.Nodes {
		c.node(n, dot)
	}
}

func (c *checker) node(n parse.Node, dot reflect.Type) {
	switch n := n.(type) {
	case *parse.TextNode, *parse.CommentNode, *parse.BreakNode, *parse.ContinueNode:
	case *parse.ActionNode:
		c.declare(n.Pipe, c.pipe(n.Pipe, dot))
	case *parse.IfNode:
		c.push()
		c.declare(n.Pipe, c.pipe(n.Pipe, dot))
		c.list(n.List, dot)
		c.list(n.ElseList, dot)
		c.pop()
	case *parse.WithNode:
		c.push()
		t := c.pipe(n.Pipe, dot)
		c.declare(n.Pipe, t)
		c.list(n.List, t)
		c.list(n.ElseList, dot)
		c.pop()
	case *parse.RangeNode:
		c.push()
		t := c.pipe(n.Pipe, dot)
		key, elem := c.rangeOf(t)
		switch len(n.Pipe.Decl) {
		case 1:
			c.set(n.Pipe.Decl[0], elem)
		case 2:
			c.set(n.Pipe.Decl[0], key)
			c.set(n.Pipe.Decl[1], elem)
		}
		c.ranges++
		if c.ranges > themeMaxRanges {
			c.errf("{{range}} inside %d others: at most %d deep", c.ranges-1, themeMaxRanges)
		}
		c.list(n.List, elem)
		c.ranges--
		c.list(n.ElseList, dot)
		c.pop()
	case *parse.TemplateNode:
		c.calls = append(c.calls, n.Name)
		if c.ranges > 0 {
			// A section renders the whole of it once per element: the page times a list.
			c.errf("{{template %q}} inside a {{range}}: call sections outside it", n.Name)
		}
		if !slices.Contains(ThemeSections, strings.TrimPrefix(n.Name, "core:")) {
			c.errf("{{template %q}}: not a section of the page", n.Name)
		}
		if n.Pipe == nil || c.pipe(n.Pipe, dot) != c.root {
			c.errf("{{template %q}}: hand it the page, {{template %q .}} at the top or {{template %q $}} inside", n.Name, n.Name, n.Name)
		}
	default:
		c.errf("%s: not supported in a theme", n)
	}
}

// rangeOf is what ranging over t yields: the key and the element.
func (c *checker) rangeOf(t reflect.Type) (key, elem reflect.Type) {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == nil:
		c.errf("{{range}} over something that is not one of the page's lists")
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		return reflect.TypeOf(0), t.Elem()
	case t.Kind() == reflect.Map:
		return t.Key(), t.Elem()
	default:
		c.errf("{{range}} over %s: only over the page's lists", t)
	}
	return nil, nil
}

func (c *checker) declare(p *parse.PipeNode, t reflect.Type) {
	if p == nil {
		return
	}
	for _, v := range p.Decl {
		if p.IsAssign {
			if _, ok := c.lookup(v.Ident[0]); !ok {
				c.errf("%s is not declared", v.Ident[0])
			}
			continue
		}
		c.set(v, t)
	}
}

func (c *checker) set(v *parse.VariableNode, t reflect.Type) {
	c.scopes[len(c.scopes)-1][v.Ident[0]] = t
}

func (c *checker) pipe(p *parse.PipeNode, dot reflect.Type) reflect.Type {
	if p == nil {
		return nil
	}
	var t reflect.Type
	for i, cmd := range p.Cmds {
		t = c.cmd(cmd, dot, t, i > 0)
	}
	return t
}

func (c *checker) cmd(cmd *parse.CommandNode, dot, prev reflect.Type, piped bool) reflect.Type {
	args := cmd.Args
	extra := len(args) - 1
	if piped {
		extra++
	}
	switch f := args[0].(type) {
	case *parse.FieldNode:
		return c.fields(dot, f.Ident, args[1:], extra)
	case *parse.ChainNode:
		return c.fields(c.arg(f.Node, dot), f.Field, args[1:], extra)
	case *parse.VariableNode:
		t, ok := c.lookup(f.Ident[0])
		if !ok {
			c.errf("%s is not declared", f.Ident[0])
		}
		return c.fields(t, f.Ident[1:], args[1:], extra)
	case *parse.IdentifierNode:
		var types []reflect.Type
		for _, a := range args[1:] {
			types = append(types, c.arg(a, dot))
		}
		if piped {
			types = append(types, prev)
		}
		return funcResult(f.Ident, types)
	}
	if extra > 0 {
		c.errf("%s is not a function", args[0])
	}
	return c.arg(args[0], dot)
}

func (c *checker) arg(n parse.Node, dot reflect.Type) reflect.Type {
	switch n := n.(type) {
	case *parse.DotNode:
		return dot
	case *parse.FieldNode:
		return c.fields(dot, n.Ident, nil, 0)
	case *parse.ChainNode:
		return c.fields(c.arg(n.Node, dot), n.Field, nil, 0)
	case *parse.VariableNode:
		t, ok := c.lookup(n.Ident[0])
		if !ok {
			c.errf("%s is not declared", n.Ident[0])
		}
		return c.fields(t, n.Ident[1:], nil, 0)
	case *parse.PipeNode:
		return c.pipe(n, dot)
	case *parse.StringNode:
		return reflect.TypeOf("")
	case *parse.NumberNode:
		if n.IsInt {
			return reflect.TypeOf(0)
		}
		return reflect.TypeOf(0.0)
	case *parse.BoolNode:
		return reflect.TypeOf(false)
	case *parse.NilNode:
		return nil
	}
	c.errf("%s: not supported in a theme", n)
	return nil
}

// fields follows .A.B.C from t; the last may be a method taking args.
func (c *checker) fields(t reflect.Type, names []string, args []parse.Node, nargs int) reflect.Type {
	for i, name := range names {
		n := 0
		if i == len(names)-1 {
			n = nargs
		}
		if name == "Asset" && i == len(names)-1 && t == c.root {
			c.asset(args)
		}
		t = c.field(t, name, n)
	}
	for _, a := range args {
		c.arg(a, nil)
	}
	return t
}

func (c *checker) asset(args []parse.Node) {
	if len(args) != 1 {
		return
	}
	s, ok := args[0].(*parse.StringNode)
	if !ok {
		c.errf("Asset takes a file name in quotes: {{$.Asset \"bg.webp\"}}")
		return
	}
	if _, ok := c.files[s.Text]; !ok || IsThemeTemplate(s.Text) {
		c.errf("Asset %q: theme/%s is not in the package", s.Text, s.Text)
	}
}

func (c *checker) field(t reflect.Type, name string, nargs int) reflect.Type {
	if t == nil {
		return nil // what an and/or yields: not followed further
	}
	ptr := t
	if t.Kind() != reflect.Pointer {
		ptr = reflect.PointerTo(t)
	}
	if m, ok := ptr.MethodByName(name); ok {
		if in := m.Type.NumIn() - 1; in != nargs {
			c.errf(".%s takes %d argument(s)", name, in)
		}
		if m.Type.NumOut() == 0 {
			return nil
		}
		return m.Type.Out(0)
	}
	base := t
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	switch base.Kind() {
	case reflect.Struct:
		f, ok := base.FieldByName(name)
		if !ok || !f.IsExported() {
			c.errf(".%s: the page has no such field here (%s)", name, typeName(base))
			return nil
		}
		if nargs > 0 {
			c.errf(".%s is not a method", name)
		}
		return f.Type
	case reflect.Map:
		if base.Key().Kind() != reflect.String {
			c.errf(".%s of %s", name, typeName(base))
			return nil
		}
		return base.Elem()
	case reflect.Interface:
		return nil
	}
	c.errf(".%s of %s, which has no fields", name, typeName(base))
	return nil
}

func typeName(t reflect.Type) string {
	if t == reflect.TypeOf(pageData{}) {
		return "the page"
	}
	return strings.TrimPrefix(t.String(), "sub.")
}

// funcResult is what a template function returns given its arguments' types.
func funcResult(name string, args []reflect.Type) reflect.Type {
	switch name {
	case "eq", "ne", "lt", "le", "gt", "ge", "not":
		return reflect.TypeOf(false)
	case "len":
		return reflect.TypeOf(0)
	case "print", "printf", "println", "html", "js", "urlquery":
		return reflect.TypeOf("")
	case "slice":
		if len(args) > 0 {
			return args[0]
		}
	case "index":
		if len(args) == 0 {
			return nil
		}
		t := args[0]
		for range args[1:] {
			for t != nil && t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			if t == nil {
				return nil
			}
			switch t.Kind() {
			case reflect.Slice, reflect.Array, reflect.Map:
				t = t.Elem()
			default:
				return nil
			}
		}
		return t
	case "and", "or":
		if len(args) > 0 && !slices.ContainsFunc(args, func(t reflect.Type) bool { return t != args[0] }) {
			return args[0]
		}
	}
	return nil
}

// flatten is the template's markup as the markup check reads it: an action is
// themeMark, a {{template}} tplMark, and every edge of an if, with or range (its
// start, else and end) ctlMark — with every branch kept, one after the other. Where
// the marks land says what the template may do there: a branch edge inside a tag
// could put a different attribute there than the check saw (html/template lets an
// empty branch join an attribute name), and a section called inside a tag would
// write its text unescaped into it. So both stay between elements; a branch may also
// sit inside an attribute's value, unless that is an address or a style.
func flatten(l *parse.ListNode, b *strings.Builder) {
	if l == nil {
		return
	}
	for _, n := range l.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
			b.Write(n.Text)
		case *parse.IfNode:
			branches(b, n.List, n.ElseList)
		case *parse.WithNode:
			branches(b, n.List, n.ElseList)
		case *parse.RangeNode:
			branches(b, n.List, n.ElseList)
		case *parse.TemplateNode:
			b.WriteString(tplMark)
		case *parse.CommentNode, *parse.BreakNode, *parse.ContinueNode:
		default:
			b.WriteString(themeMark)
		}
	}
}

func branches(b *strings.Builder, list, elseList *parse.ListNode) {
	b.WriteString(ctlMark)
	flatten(list, b)
	b.WriteString(ctlMark)
	flatten(elseList, b)
	b.WriteString(ctlMark)
}

const (
	ctlMark = "rpthemectl"
	tplMark = "rpthemetpl"
	// endMark closes a template's markup for the check: a template that ends
	// inside a tag swallows it, and the check says so.
	endMark = "<i data-rpthemeend></i>"
)

func hasMark(s string) bool {
	return strings.Contains(s, themeMark) || strings.Contains(s, ctlMark) || strings.Contains(s, tplMark)
}

// themeTags are the elements a theme may use: text, layout, lists, tables, images
// and inline SVG. Nothing that runs or embeds (script, iframe, object), loads
// (link, style, meta) or sends (form, input).
var themeTags = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`a abbr article aside b bdi bdo blockquote br button caption cite code col colgroup
		dd del details dfn div dl dt em figcaption figure footer h1 h2 h3 h4 h5 h6 header hr i img ins kbd label li
		main mark meter nav ol p pre progress q s samp section small span strong sub summary sup table tbody td tfoot
		input select option optgroup
		th thead time tr u ul var wbr
		svg g path circle ellipse line polyline polygon rect defs lineargradient radialgradient stop symbol use
		clippath mask pattern text tspan`) {
		themeTags[t] = true
	}
}

var (
	attrNameRe = regexp.MustCompile(`^[a-z][a-z0-9_.:-]*$`)
	// Attributes that load, send or switch the page by themselves.
	themeBadAttrs = map[string]bool{
		"srcset": true, "srcdoc": true, "action": true, "formaction": true, "ping": true, "http-equiv": true,
		"form": true, "attributename": true,
		"name": true, // <img name="getElementById"> would replace the page script's own
	}
	themeLinkAttrs = map[string]bool{"href": true, "xlink:href": true, "cite": true}
	themeSrcAttrs  = map[string]bool{"src": true, "poster": true, "background": true, "data": true}
)

// checkMarkup reads a theme template's markup the way a browser would and lists
// what is not allowed in it.
func checkMarkup(s string) []string {
	var out []string
	add := func(format string, args ...any) {
		if len(out) < 20 {
			out = append(out, fmt.Sprintf(format, args...))
		}
	}
	z := html.NewTokenizer(strings.NewReader(s + endMark))
	ended := false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if !ended {
				add("the template ends inside a tag or an attribute")
			}
			return out
		case html.CommentToken:
			if hasMark(string(z.Text())) {
				add("no {{…}} inside an HTML comment")
			}
		case html.DoctypeToken:
			add("<!doctype> is the page's, not a section's")
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			tag := tok.Data
			if tt == html.StartTagToken && tag == "i" && len(tok.Attr) == 1 && tok.Attr[0].Key == "data-rpthemeend" {
				ended = true
				continue
			}
			if hasMark(tag) {
				add("an element's name cannot come from {{…}}")
				continue
			}
			if !themeTags[tag] {
				add("<%s> is not allowed in a theme", tag)
				continue
			}
			if tt == html.EndTagToken {
				continue
			}
			for _, a := range tok.Attr {
				for _, e := range checkAttr(tag, a.Key, a.Val) {
					add("<%s %s>: %s", tag, a.Key, e)
				}
			}
		}
	}
}

func checkAttr(tag, key, val string) []string {
	switch {
	case hasMark(key):
		return []string{"no {{…}} between a tag's attributes: put {{if}} around the whole element, or inside an attribute's value"}
	case strings.Contains(val, tplMark):
		return []string{"no {{template}} inside a tag: call sections between elements"}
	case strings.Contains(val, ctlMark) && (themeLinkAttrs[key] || themeSrcAttrs[key] || key == "style"):
		return []string{"no {{if}}, {{range}} or {{with}} inside an address or a style: choose the whole element instead"}
	case strings.HasPrefix(key, "on"):
		return []string{"no event handlers in a theme: a button says what it does in data-action"}
	case !attrNameRe.MatchString(key):
		return []string{"not an attribute name"}
	case themeBadAttrs[key]:
		return []string{"not allowed in a theme"}
	case key == "style":
		low := strings.ToLower(strings.ReplaceAll(val, themeMark, "0"))
		for _, bad := range []string{"url(", "@import", "expression(", "javascript:", "behavior", "-moz-binding", `\`, "image-set(", "://"} {
			if strings.Contains(low, bad) {
				return []string{fmt.Sprintf("%q: put it in theme.css", bad)}
			}
		}
	case tag == "input" && key == "type":
		if !slices.Contains([]string{"text", "number", "checkbox", "search", "hidden"}, strings.ToLower(strings.TrimSpace(val))) {
			return []string{"an input is text, number, checkbox, search or hidden"}
		}
	case key == "data-action":
		if !strings.Contains(val, themeMark) && !slices.Contains(ThemeActions, val) {
			return []string{fmt.Sprintf("%q is not an action (%s)", val, strings.Join(ThemeActions, ", "))}
		}
	}
	if !themeLinkAttrs[key] && !themeSrcAttrs[key] {
		return nil
	}
	if tag == "use" && !strings.HasPrefix(strings.TrimSpace(val), "#") {
		return []string{"an SVG <use> points into the page: href=\"#id\""}
	}
	v := urlNorm(val)
	for _, form := range []string{strings.ReplaceAll(v, themeMark, ""), strings.ReplaceAll(v, themeMark, "x")} {
		if strings.Contains(form, "javascript:") || strings.Contains(form, "vbscript:") {
			return []string{"a script address"}
		}
	}
	slot := strings.HasPrefix(v, themeMark)
	if themeSrcAttrs[key] {
		if !slot && !strings.HasPrefix(v, "data:image/") {
			return []string{`only the page's own addresses: {{$.Asset "name"}}, {{.SubURL}}/…, or data:image/`}
		}
		return nil
	}
	if strings.Contains(strings.ReplaceAll(v, themeMark, ""), "data:") {
		return []string{"a data: link"}
	}
	for _, ok := range []string{themeMark, "#", "https://", "http://", "mailto:", "tg://", "/"} {
		if strings.HasPrefix(v, ok) {
			return nil
		}
	}
	return []string{"a link is https://…, mailto:, tg://, #… or from {{…}}"}
}

// urlNorm is an address as a browser reads its scheme: lower case, without the
// whitespace and control characters it skips.
func urlNorm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r <= ' ' || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
