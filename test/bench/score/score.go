// Package score is how the bench and the probes score what deepcode writes:
// DeepWiki's topics of each bench repository and whether a wiki's headings
// cover them (this file), and the judge reading prose for self-talk and its
// language (judge.go).
package score

import (
	"regexp"
	"slices"
	"strings"
)

// Topic is a subject of DeepWiki's wiki of a repository, covered when a page's
// title or one of its "##" or "###" headings holds one of Words, as whole
// words, case aside, a word of a name like WelcomeController counting.
type Topic struct {
	Name  string
	Words []string
}

// Topics are DeepWiki's topics of each bench repository, by its name,
// glossary and governance aside.
var Topics = map[string][]Topic{
	"chi": {
		{"Radix tree", []string{"radix", "tree"}}, {"Mux", []string{"mux"}},
		{"Router interface", []string{"router interface", "routes interface", "interface"}},
		{"Context and URLParam", []string{"urlparam", "url parameters", "route context", "routing context", "context"}},
		{"Middleware and custom middleware", []string{"middleware", "middlewares"}},
		{"Logger and Recoverer", []string{"logger", "logging", "recoverer", "panic recovery"}}, {"Compress", []string{"compress", "compression"}},
		{"Throttle and Timeout", []string{"throttle", "throttling", "timeout", "timeouts"}},
		{"CleanPath and StripSlashes", []string{"cleanpath", "stripslashes", "clean path", "path cleaning", "strip slashes", "path rewrites"}},
		{"RealIP", []string{"realip", "real ip", "client ip"}}, {"Profiler", []string{"profiler", "profiling", "pprof"}},
		{"WrapResponseWriter", []string{"wrapresponsewriter", "response writer", "response writers"}},
		{"REST example", []string{"rest"}}, {"FileServer", []string{"fileserver", "file server", "file serving", "static files"}},
		{"API versioning", []string{"versioning", "versions", "api version"}}, {"Graceful shutdown", []string{"graceful", "shutdown"}},
		{"Testing and build", []string{"testing", "tests", "ci", "build"}},
	},
	"zustand": {
		{"create", []string{"create"}}, {"createStore", []string{"createstore", "vanilla store", "vanilla stores"}},
		{"setState and updating state", []string{"setstate", "state updates", "updating state", "update state"}},
		{"Selectors and re-renders", []string{"selector", "selectors", "re-render", "re-renders", "rerenders"}},
		{"subscribe", []string{"subscribe", "subscriptions", "subscription", "subscribers"}}, {"persist", []string{"persist", "persistence"}},
		{"devtools", []string{"devtools"}}, {"immer", []string{"immer"}}, {"redux", []string{"redux"}},
		{"subscribeWithSelector", []string{"subscribewithselector"}}, {"combine", []string{"combine"}},
		{"createWithEqualityFn", []string{"createwithequalityfn", "equality function", "equality functions"}}, {"useStore", []string{"usestore"}},
		{"shallow and useShallow", []string{"shallow", "useshallow"}}, {"StoreApi", []string{"storeapi", "store api"}},
		{"TypeScript", []string{"typescript"}}, {"SSR", []string{"ssr", "server-side rendering", "server side rendering", "hydration"}},
		{"React Context", []string{"context"}}, {"Reset state", []string{"reset", "resetting"}}, {"Slices", []string{"slice", "slices"}},
		{"Auto-generating selectors", []string{"auto-generating", "auto-generated", "createselectors", "generated selectors"}},
		{"Flux", []string{"flux"}}, {"Transient updates", []string{"transient"}}, {"Build with Rollup", []string{"rollup", "build"}},
		{"Module formats and exports", []string{"exports", "module formats", "esm", "commonjs", "cjs", "entry points"}},
		{"Testing", []string{"testing", "tests"}}, {"CI", []string{"ci", "continuous integration", "github actions", "workflows"}},
		{"Demo app", []string{"demo"}},
	},
	"express": {
		{"Application", []string{"application", "app"}}, {"Request", []string{"request", "requests"}}, {"Response", []string{"response", "responses"}},
		{"Request-response lifecycle", []string{"lifecycle", "request handling", "request flow", "request-response", "dispatch", "dispatching"}},
		{"Settings", []string{"settings", "app.set", "configuration"}}, {"Router and Route", []string{"router", "route", "routes", "routing"}},
		{"Path parameters and app.param", []string{"app.param", "params", "parameters"}},
		{"Route error handling", []string{"error handling", "error-handling", "errors"}}, {"Middleware flow", []string{"middleware"}},
		{"Body parsing", []string{"body parsing", "body parser", "body-parser", "bodies", "json", "urlencoded"}},
		{"Static files", []string{"static"}}, {"res.send and res.json", []string{"res.send", "res.json", "send", "sending"}},
		{"sendFile and download", []string{"sendfile", "download", "downloads", "file transfer"}}, {"Redirect", []string{"redirect", "redirects", "redirection"}},
		{"Cookies", []string{"cookie", "cookies"}}, {"Headers and caching", []string{"headers", "caching", "cache", "etag", "freshness"}},
		{"View resolution", []string{"view", "views", "view lookup"}}, {"Template rendering", []string{"template", "templates", "rendering", "render"}},
		{"Auth and session examples", []string{"auth", "authentication", "session", "sessions"}},
		{"Error pages", []string{"error pages", "error page", "404"}}, {"REST and content negotiation", []string{"rest", "content negotiation", "content-negotiation"}},
		{"Testing and CI", []string{"testing", "tests", "ci"}},
	},
	"eshop": {
		{"Architecture", []string{"architecture"}}, {"Technology stack", []string{"technology", "technologies", "tech stack", "stack"}},
		{"Solution organization", []string{"solution", "project structure", "repository structure", "organization", "projects"}},
		{"Central package management", []string{"package management", "directory.packages.props", "central package", "packages", "dependencies"}},
		{"ServiceDefaults", []string{"servicedefaults", "service defaults"}}, {"AppHost", []string{"apphost", "aspire", "orchestration"}},
		{"Catalog.API", []string{"catalog"}}, {"Basket.API", []string{"basket"}}, {"Ordering.API", []string{"ordering"}},
		{"Identity.API", []string{"identity"}}, {"Webhooks.API", []string{"webhook", "webhooks"}},
		{"Mobile BFF", []string{"bff", "backend for frontend", "mobile-bff"}},
		{"Background workers", []string{"orderprocessor", "paymentprocessor", "order processor", "payment processor", "background", "workers"}},
		{"WebApp", []string{"webapp", "web app", "web application", "blazor web", "storefront"}}, {"MAUI ClientApp", []string{"maui", "clientapp", "client app"}},
		{"HybridApp", []string{"hybrid", "hybridapp"}},
		{"Shared UI components", []string{"webappcomponents", "shared components", "ui components", "components"}},
		{"Event bus", []string{"event bus", "eventbus", "rabbitmq", "integration events"}},
		{"AI integration", []string{"ai", "embedding", "embeddings", "semantic search", "chatbot"}},
		{"Authentication", []string{"authentication", "auth", "openid", "openid connect", "jwt"}},
		{"OpenAPI and API versioning", []string{"openapi", "versioning", "swagger", "api version"}},
		{"Configuration", []string{"configuration", "settings"}},
		{"Unit, functional and E2E testing", []string{"testing", "tests", "e2e", "unit tests", "functional tests"}},
		{"CI/CD", []string{"ci", "ci/cd", "workflows", "github actions", "pipelines"}},
		{"Azure deployment", []string{"azure", "deployment", "deploying", "azd"}},
	},
	"petclinic": {
		{"Maven", []string{"maven", "pom.xml"}}, {"Gradle", []string{"gradle"}}, {"Build wrappers", []string{"wrapper", "wrappers", "mvnw", "gradlew"}},
		{"CI", []string{"ci", "continuous integration", "github actions", "workflows"}},
		{"Entity hierarchy", []string{"baseentity", "base entity", "hierarchy", "base classes"}},
		{"Core entities", []string{"entities", "entity", "domain model"}}, {"Validation", []string{"validation", "validator", "validators"}},
		{"Owner, Pet and Vet controllers", []string{"controller", "controllers"}},
		{"System controllers", []string{"welcome", "crash", "system controllers"}}, {"Formatter", []string{"formatter", "formatters"}},
		{"Repository layer", []string{"repository", "repositories"}}, {"Caching", []string{"caching", "cache"}},
		{"Schema and data initialization", []string{"schema", "schemas", "seed data", "data initialization", "sql"}},
		{"Multiple databases", []string{"mysql", "postgres", "postgresql", "databases", "database profiles"}},
		{"Runtime configuration", []string{"configuration", "properties", "application.properties"}},
		{"Thymeleaf", []string{"thymeleaf", "templates"}},
		{"Static resources", []string{"static", "css", "scss", "styles", "stylesheet", "stylesheets"}},
		{"Internationalization", []string{"i18n", "internationalization", "internationalized", "localization", "message bundles"}},
		{"Unit tests", []string{"unit tests", "unit testing", "controller tests", "tests"}},
		{"Integration tests", []string{"integration tests", "integration testing", "testcontainers"}},
		{"Performance testing", []string{"jmeter", "performance"}},
		{"Dev container", []string{"devcontainer", "dev container", "development environment", "dev environment"}},
		{"Code style", []string{"checkstyle", "code style", "nohttp", "javaformat", "formatting"}},
	},
	"gson": {
		{"Gson class", []string{"gson class", "gson facade", "gson entry point", "gson object", "gson instance", "core gson"}},
		{"GsonBuilder", []string{"gsonbuilder", "gson builder"}},
		{"TypeAdapter system", []string{"typeadapter", "typeadapters", "typeadapterfactory", "type adapter", "type adapters"}},
		{"JSON object model", []string{"jsonelement", "jsonobject", "tree model", "object model"}},
		{"Streaming", []string{"streaming", "stream", "jsonreader", "jsonwriter"}},
		{"Type handling with TypeToken", []string{"typetoken", "generics", "generic types"}},
		{"Collections and maps", []string{"collection", "collections", "map", "maps"}},
		{"Exclusion strategies", []string{"exclusion", "exclusionstrategy", "excluder"}},
		{"Field naming", []string{"field naming", "fieldnamingpolicy", "serializedname", "naming"}},
		{"Versioning and annotations", []string{"versioning", "annotations", "expose", "since", "until"}},
		{"Protobuf integration", []string{"protobuf", "proto", "protocol buffers"}},
		{"Date and time", []string{"date", "dates", "time", "java.time"}},
		{"Build and testing", []string{"build", "testing", "tests", "maven"}},
		{"Module system", []string{"jpms", "module system", "module-info", "modules", "module descriptor"}},
		{"Troubleshooting", []string{"troubleshooting"}},
	},
	"axum": {
		{"Crate architecture", []string{"architecture", "crates", "workspace", "crate structure"}},
		{"Serving and connections", []string{"serve", "serving", "server", "connection", "connections"}},
		{"ConnectInfo", []string{"connectinfo", "connect info", "client address"}}, {"Router configuration", []string{"router", "routing"}},
		{"Path matching", []string{"path matching", "route matching", "matchit", "pathrouter", "path router", "path routing", "matching"}},
		{"Method routing", []string{"methodrouter", "method router", "method routing"}}, {"Handler", []string{"handler", "handlers"}},
		{"Typed paths", []string{"typed path", "typed paths", "typedpath"}}, {"Fallback", []string{"fallback", "fallbacks"}},
		{"FromRequest and FromRequestParts", []string{"fromrequest", "fromrequestparts", "extractor", "extractors", "extraction"}},
		{"Path and Query", []string{"path", "query"}}, {"Json and Form", []string{"json", "form"}}, {"State", []string{"state"}},
		{"Multipart", []string{"multipart"}}, {"IntoResponse", []string{"intoresponse", "into response", "responses"}},
		{"Tuple responses", []string{"tuple", "tuples", "response parts", "intoresponseparts"}},
		{"Streaming", []string{"streaming", "stream", "streams", "body"}}, {"axum-extra responses", []string{"axum-extra", "extra"}},
		{"WebSocket", []string{"websocket", "websockets"}}, {"SSE", []string{"sse", "server-sent events"}},
		{"Tower middleware", []string{"tower", "layer", "layers", "middleware"}},
		{"from_fn and custom middleware", []string{"from_fn", "fromfn", "custom middleware", "function middleware", "middleware functions"}},
		{"with_state and FromRef", []string{"with_state", "fromref"}}, {"Extension", []string{"extension", "extensions"}},
		{"Errors and rejections", []string{"rejection", "rejections", "error handling", "errors"}},
		{"Macros", []string{"macro", "macros", "debug_handler", "derive"}}, {"Testing and CI", []string{"testing", "tests", "ci"}},
		{"Examples", []string{"example", "examples"}}, {"Graceful shutdown", []string{"graceful", "shutdown"}},
	},
}

// Cover is how many of topics the headings cover, and the names of those they
// do not.
func Cover(topics []Topic, heads []string) (int, []string) {
	n, missing := 0, []string(nil)
	for _, tp := range topics {
		found := false
		for _, h := range heads {
			for _, w := range tp.Words {
				found = found || holds(h, w)
			}
		}
		if found {
			n++
		} else {
			missing = append(missing, tp.Name)
		}
	}
	return n, missing
}

// holds reports whether heading holds word as a whole word, case, hyphens
// and inflections aside (inflected), a name's words counting (edge):
// WelcomeController and HTTPServer hold welcome and server, Auto Generating
// holds auto-generating, Versioned APIs holds versioning, Unwelcome does not
// hold welcome.
func holds(heading, word string) bool {
	space := strings.NewReplacer("-", " ") // one byte for one, so that edge reads heading where h is
	h := space.Replace(lowerASCII(heading))
	re, tails := inflected(space.Replace(strings.ToLower(word)))
	for _, m := range re.FindAllStringIndex(h, -1) {
		// Each ending is tried, as one may take a name's next word: Route|Definition, not Routed|efinition.
		if edge(heading, m[0]) && slices.ContainsFunc(tails, func(t string) bool { return strings.HasPrefix(h[m[1]:], t) && edge(heading, m[1]+len(t)) }) {
			return true
		}
	}
	return false
}

// endings are what an inflection adds to a word's stem, the longest first.
var endings = []string{"ing", "ies", "ied", "es", "ed", "s", "y", "e"}

// inflected matches phrase up to the stem of its last word, each word before
// it of letters alone in any inflection, its stem and an ending or none, and
// returns the endings the last word may take: none first, and only that for a
// name (app.param), which stays as written.
func inflected(phrase string) (*regexp.Regexp, []string) {
	words, tails := strings.Fields(phrase), []string{""}
	for i, w := range words {
		if strings.Trim(w, "abcdefghijklmnopqrstuvwxyz") != "" {
			words[i] = regexp.QuoteMeta(w)
			continue
		}
		s, cut := stem(w)
		last := s[len(s)-1:]
		ends := append([]string{last + "ings", last + "ing", last + "ed", "ings"}, endings...) // mappings, mapping, mapped, settings
		if cut == "" || cut == "s" {
			ends = ends[:len(ends)-2] // y and e end a word that lost one: stat or stats is no State, nor auth Authy
		}
		if i == len(words)-1 {
			words[i], tails = s, append(tails, ends...)
		} else {
			words[i] = s + "(?:" + strings.Join(ends, "|") + ")?"
		}
	}
	return regexp.MustCompile(strings.Join(words, " ")), tails
}

// stem is word without the ending an inflection added, three letters left
// at least, four after ing, ed or es (string, embed and files keep theirs):
// version of versioned, versioning and versions, cach of cache and caches,
// map of mapping, its consonant undoubled. An s after s or u (class, status)
// is the word's own, as is a y after a vowel (play, key). It also returns the
// ending cut, "" for none.
func stem(word string) (string, string) {
	for _, end := range endings {
		s, least := strings.TrimSuffix(word, end), 3
		if end == "ing" || end == "ed" || end == "es" {
			least = 4
		}
		if len(s) == len(word) || len(s) < least || end == "s" && strings.ContainsAny(s[len(s)-1:], "su") ||
			end == "y" && strings.ContainsAny(s[len(s)-1:], "aeiou") {
			continue
		}
		if n := len(s); (end == "ing" || end == "ed") && n >= 4 && s[n-1] == s[n-2] && !strings.ContainsAny(s[n-1:], "aeiouls") {
			s = s[:n-1] // calling and passed keep theirs
		}
		return s, end
	}
	return word, ""
}

// edge reports whether a word may start or end at byte i of s: at an end of
// s, beside a byte that is no ASCII letter or digit (so beside any Chinese
// character), between a letter and a digit (OAuth|2, Base|64), before a
// capital that follows a small letter or digit (Welcome|Controller), before
// a capital that ends a run of them and is followed by a small letter
// (HTTP|Server, CI|Pipeline), or before an s after a run of capitals that
// ends a word (API|s, API|sController).
func edge(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	a, b := s[i-1], s[i]
	capital := func(c byte) bool { return 'A' <= c && c <= 'Z' }
	small := func(c byte) bool { return 'a' <= c && c <= 'z' }
	digit := func(c byte) bool { return '0' <= c && c <= '9' }
	next := byte(' ')
	if i+1 < len(s) {
		next = s[i+1]
	}
	return !alnum(a) || !alnum(b) || !digit(a) && digit(b) || (small(a) || digit(a)) && capital(b) ||
		capital(a) && capital(b) && small(next) || i >= 2 && capital(s[i-2]) && capital(a) && b == 's' && (!alnum(next) || capital(next))
}

// alnum reports whether c is an ASCII letter or digit.
func alnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// lowerASCII lowers s's ASCII letters only, so that its bytes stay where they are.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// Headings are the "##" and "###" headings of a page's text (not "####"),
// outside code blocks, where "## " may be a shell comment. A block opens with
// three or more backticks or tildes, indented up to three spaces, and closes
// with a line of as many or more of the same and nothing else.
func Headings(content string) []string {
	var out []string
	fence := "" // the open block's mark
	for line := range strings.Lines(content) {
		m := fenceRe.FindStringSubmatch(line)
		switch {
		case m != nil && fence == "":
			fence = m[1]
		case m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(m[2]) == "":
			fence = ""
		case fence == "":
			if h, ok := strings.CutPrefix(line, "## "); ok || strings.HasPrefix(line, "### ") {
				out = append(out, strings.TrimSpace(strings.TrimPrefix(h, "### ")))
			}
		}
	}
	return out
}

// fenceRe matches a code block's fence line: its mark ("```", "~~~~") and
// what follows it on the line ("go").
var fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})([^\n]*)")
