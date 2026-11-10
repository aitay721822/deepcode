package bench

import (
	"slices"
	"strings"
	"testing"
	"unicode"
)

// target is a benchmark repository: DeepWiki and Code Wiki both have its wiki.
// It is indexed at base, then updated to head (one small commit later), where
// the questions are asked and the retrieval eval runs (its corpus pins head).
// Its topics are those of DeepWiki's wiki of it, glossary and governance aside.
type target struct {
	name, url, base, head string
	questions             []question
	topics                []topic
}

// topic is a subject of DeepWiki's wiki that head's wiki covers when a page's
// title or one of its "##" or "###" headings holds one of words, as whole
// words, case aside, a word of a name like WelcomeController counting.
type topic struct {
	name  string
	words []string
}

// question is asked at head; its answer passes when it holds every word of
// want, case aside: what an answer from the right code cannot leave out,
// names of that code rather than words any answer would use. A word may list
// alternatives, a|b, for a name written more than one way. A word the question
// itself holds checks nothing, so each question has one that it does not.
// lang is the language it is asked in, as the judge names one; "" for English,
// Japanese when it holds kana, or Chinese when it holds Han (chinese).
type question struct {
	ask  string
	want []string
	lang string
}

// language is the language q is asked in, so its answer's.
func (q question) language() string {
	switch {
	case q.lang != "":
		return q.lang
	case strings.ContainsFunc(q.ask, func(r rune) bool { return unicode.In(r, unicode.Hiragana, unicode.Katakana) }):
		return "Japanese"
	case chinese(q.ask):
		return "Chinese"
	}
	return "English"
}

var targets = []target{
	{"chi", "https://github.com/go-chi/chi", "04f7f55a7b5ddb2ecd79a8d813b2256c1113b8a9", "9f8adb1aabaa8a91180a821111d3f8e668d1c0bf", []question{
		{"How does chi's router match a request path to a handler, including URL parameters?", []string{"findRoute", "URLParam"}, ""},
		{"How do middlewares work in chi, and how are they chained with Use, With and Group?", []string{"inline", "chain.go|chain("}, ""},
		{"How does chi mount a sub-router, and how does the routing context reach it?", []string{"Mount", "RoutePath"}, ""},
		{"Which middlewares does chi ship, and how does the Timeout middleware work?", []string{"Timeout", "WithTimeout"}, ""},
		{"How can I get the matched route pattern of a request, for example for metrics?", []string{"RoutePattern"}, ""},
		{"chi のルーターは、URL パラメータを含め、リクエストのパスをどのようにハンドラーに対応付けますか？", []string{"findRoute", "URLParam"}, "Japanese"},
		{"chi でサブルーターを Mount すると、ルーティングのコンテキストはどのようにそこへ渡されますか？", []string{"Mount", "RoutePath"}, "Japanese"},
		{"Wie funktionieren Middlewares in chi, und wie werden sie mit Use, With und Group verkettet?", []string{"inline", "chain.go|chain("}, "German"},
		{"Welche Middlewares liefert chi mit, und wie funktioniert die Timeout-Middleware?", []string{"Timeout", "WithTimeout"}, "German"},
	}, []topic{
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
	}},
	{"zustand", "https://github.com/pmndrs/zustand", "b4883239492c687244d92cf50bf4fae7125ae5ad", "382d29c485e67018c06e75ec7235130d0968522c", []question{
		{"How does create build a store and its React hook from a state creator?", []string{"createStore", "useStore"}, ""},
		{"How does setState merge an update and notify the subscribers?", []string{"Object.assign", "listener"}, ""},
		{"How does the persist middleware save the state and rehydrate it?", []string{"setItem", "getItem|onRehydrateStorage|createJSONStorage"}, ""},
		{"How does zustand avoid needless re-renders with selectors and useShallow?", []string{"useShallow", "useSyncExternalStore"}, ""},
		{"How do the devtools and immer middlewares wrap set?", []string{"produce(|`produce`", "connect(|__REDUX_DEVTOOLS_EXTENSION__"}, ""},
	}, []topic{
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
	}},
	{"express", "https://github.com/expressjs/express", "6389ae5e397acf28dd66765fbcd9c487f7a61e17", "43fca52d28a422c8be48fd489cbd4f2d6152bab6", []question{
		{"How does Express route a request through app.use and app.get to its handler?", []string{"app.use", "app.handle|router.handle"}, ""},
		{"How does the router handle route parameters and app.param?", []string{"app.param", "req.params|router.param"}, ""},
		{"How does Express render a view with a template engine?", []string{"app.render", "app.engine", "view.js|new View"}, ""},
		{"How are errors passed to error-handling middleware?", []string{"next(err", "err, req, res, next|four arguments|4 arguments|fn.length"}, ""},
		{"What does res.send do with strings, Buffers and objects?", []string{"Buffer", "res.json|this.json", "Content-Type"}, ""},
	}, []topic{
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
	}},
	{"eshop", "https://github.com/dotnet/eShop", "dc7ea499cd356924fb6689b3702964a5869dbae9", "913acea66708a59595351174cee9b29f37179d29", []question{
		{"Which services make up eShop, and how does .NET Aspire orchestrate them?", []string{"AppHost", "AddProject|AddDotnetProject"}, ""}, // head's AppHost.cs calls its wrapper
		{"How does the Ordering service take an order from basket checkout to payment?", []string{"OrderPaymentSucceededIntegrationEvent", "SetPaidOrderStatusCommand"}, ""},
		{"How do the services communicate through integration events and the event bus?", []string{"IntegrationEvent", "IEventBus", "RabbitMQ"}, ""},
		{"How is the catalog's semantic search with AI embeddings implemented?", []string{"GetEmbeddingAsync", "CosineDistance"}, ""},
		{"How are identity and authentication handled across the web app and the APIs?", []string{"OpenIdConnect", "AddJwtBearer|JwtBearer"}, ""},
		{"eShop はどのサービスで構成され、.NET Aspire はそれらをどのように編成していますか？", []string{"AppHost", "AddProject|AddDotnetProject"}, "Japanese"},
		{"Ordering サービスは、バスケットのチェックアウトから支払いまで、注文をどのように処理しますか？", []string{"OrderPaymentSucceededIntegrationEvent", "SetPaidOrderStatusCommand"}, "Japanese"},
		{"Wie kommunizieren die Dienste über Integrationsereignisse und den Event-Bus miteinander?", []string{"IntegrationEvent", "IEventBus", "RabbitMQ"}, "German"},
		{"Wie ist die semantische Suche des Katalogs mit KI-Embeddings umgesetzt?", []string{"GetEmbeddingAsync", "CosineDistance"}, "German"},
	}, []topic{
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
	}},
	{"petclinic", "https://github.com/spring-projects/spring-petclinic", "0d064b04958da7715a9138459bafbde3bd10949d", "500158f732419217507c7656904b8e6aa1bcc0d6", []question{
		{"How is the domain (owners, pets, visits, vets) modelled and persisted with JPA?", []string{"@Entity", "OwnerRepository"}, ""},
		{"How does OwnerController find, create and edit owners?", []string{"processFindForm", "processCreationForm"}, ""},
		{"How are the vets served both as an HTML page and as JSON or XML?", []string{"ResponseBody", "showResourcesVetList"}, ""},
		{"How is caching configured for the list of vets?", []string{"Cacheable", "EnableCaching"}, ""},
		{"How does the application validate a new pet's form?", []string{"PetValidator", "rejectValue"}, ""},
	}, []topic{
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
	}},
	{"gson", "https://github.com/google/gson", "57731049da3e415be3d5d34db1ca3b80f3c73810", "65df6e5a99747f73d386f3a61d887a0257255e0f", []question{
		{"How does Gson serialize an object to JSON by reflection?", []string{"ReflectiveTypeAdapterFactory", "BoundField"}, ""},
		{"How do TypeAdapter and TypeAdapterFactory work, and how is one chosen for a type?", []string{"getAdapter", "factories"}, ""},
		{"How does JsonReader parse JSON as a stream?", []string{"peek", "JsonToken"}, ""},
		{"How does Gson handle generic types with TypeToken?", []string{"TypeToken", "getType"}, ""},
		{"How do @SerializedName, @Expose and FieldNamingPolicy control field names?", []string{"SerializedName", "Expose", "FieldNamingPolicy", "translateName"}, ""},
	}, []topic{
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
	}},
	{"axum", "https://github.com/tokio-rs/axum", "40d061fdacd468217c7a609bede16357276b6c40", "4cc51a96df444592ec8276738d4ccd1202e6150d", []question{
		{"How does axum's Router match routes and dispatch to handlers?", []string{"PathRouter", "MethodRouter"}, ""},
		{"How do extractors such as Path, Query and Json work through FromRequest and FromRequestParts?", []string{"from_request_parts|FromRequestParts", "Rejection"}, ""},
		{"How does axum use tower middleware and layers?", []string{"layer(|`layer`|Router::layer", "route_layer|ServiceBuilder|from_fn|Layer<"}, ""},
		{"How is shared application state passed to handlers with State?", []string{"with_state", "FromRef"}, ""},
		{"How does a handler's return value become an HTTP response through IntoResponse?", []string{"IntoResponse", "into_response"}, ""},
	}, []topic{
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
	}},
}

// TestKeywords: each question has a word the question does not hold itself,
// and no word has an empty alternative, which every answer would hold.
func TestKeywords(t *testing.T) {
	for _, tg := range targets {
		for _, q := range tg.questions {
			own := false
			for _, w := range q.want {
				alts := strings.Split(strings.ToLower(w), "|")
				if slices.Contains(alts, "") {
					t.Errorf("%s: %q has an empty alternative", tg.name, w)
				}
				own = own || !slices.ContainsFunc(alts, func(a string) bool { return strings.Contains(strings.ToLower(q.ask), a) })
			}
			if !own {
				t.Errorf("%s: every word of %q is in the question", tg.name, q.ask)
			}
		}
	}
}
