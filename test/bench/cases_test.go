package bench

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode"

	"deepcode/test/bench/score"
)

// target is a benchmark repository: DeepWiki and Code Wiki both have its wiki.
// It is indexed at base, then updated to head (one small commit later), where
// the questions are asked and the retrieval eval runs (its corpus pins head).
type target struct {
	name, url, base, head string
	questions             []question
	pages                 int // DeepWiki's page count, read 2026-11-09 with read_wiki_structure
}

// topics are DeepWiki's topics of tg (score.Topics).
func (tg target) topics() []score.Topic { return score.Topics[tg.name] }

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
	}, 28},
	{"zustand", "https://github.com/pmndrs/zustand", "b4883239492c687244d92cf50bf4fae7125ae5ad", "382d29c485e67018c06e75ec7235130d0968522c", []question{
		{"How does create build a store and its React hook from a state creator?", []string{"createStore", "useStore"}, ""},
		{"How does setState merge an update and notify the subscribers?", []string{"Object.assign", "listener"}, ""},
		{"How does the persist middleware save the state and rehydrate it?", []string{"setItem", "getItem|onRehydrateStorage|createJSONStorage"}, ""},
		{"How does zustand avoid needless re-renders with selectors and useShallow?", []string{"useShallow", "useSyncExternalStore"}, ""},
		{"How do the devtools and immer middlewares wrap set?", []string{"produce(|`produce`", "connect(|__REDUX_DEVTOOLS_EXTENSION__"}, ""},
	}, 42},
	{"express", "https://github.com/expressjs/express", "6389ae5e397acf28dd66765fbcd9c487f7a61e17", "43fca52d28a422c8be48fd489cbd4f2d6152bab6", []question{
		{"How does Express route a request through app.use and app.get to its handler?", []string{"app.use", "app.handle|router.handle"}, ""},
		{"How does the router handle route parameters and app.param?", []string{"app.param", "req.params|router.param"}, ""},
		{"How does Express render a view with a template engine?", []string{"app.render", "app.engine", "view.js|new View"}, ""},
		{"How are errors passed to error-handling middleware?", []string{"next(err", "err, req, res, next|four arguments|4 arguments|fn.length"}, ""},
		{"What does res.send do with strings, Buffers and objects?", []string{"Buffer", "res.json|this.json", "Content-Type"}, ""},
	}, 33},
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
	}, 37},
	{"petclinic", "https://github.com/spring-projects/spring-petclinic", "0d064b04958da7715a9138459bafbde3bd10949d", "500158f732419217507c7656904b8e6aa1bcc0d6", []question{
		{"How is the domain (owners, pets, visits, vets) modelled and persisted with JPA?", []string{"@Entity", "OwnerRepository"}, ""},
		{"How does OwnerController find, create and edit owners?", []string{"processFindForm", "processCreationForm"}, ""},
		{"How are the vets served both as an HTML page and as JSON or XML?", []string{"ResponseBody", "showResourcesVetList"}, ""},
		{"How is caching configured for the list of vets?", []string{"Cacheable", "EnableCaching"}, ""},
		{"How does the application validate a new pet's form?", []string{"PetValidator", "rejectValue"}, ""},
	}, 35},
	{"gson", "https://github.com/google/gson", "57731049da3e415be3d5d34db1ca3b80f3c73810", "65df6e5a99747f73d386f3a61d887a0257255e0f", []question{
		{"How does Gson serialize an object to JSON by reflection?", []string{"ReflectiveTypeAdapterFactory", "BoundField"}, ""},
		{"How do TypeAdapter and TypeAdapterFactory work, and how is one chosen for a type?", []string{"getAdapter", "factories"}, ""},
		{"How does JsonReader parse JSON as a stream?", []string{"peek", "JsonToken"}, ""},
		{"How does Gson handle generic types with TypeToken?", []string{"TypeToken", "getType"}, ""},
		{"How do @SerializedName, @Expose and FieldNamingPolicy control field names?", []string{"SerializedName", "Expose", "FieldNamingPolicy", "translateName"}, ""},
	}, 23},
	{"axum", "https://github.com/tokio-rs/axum", "40d061fdacd468217c7a609bede16357276b6c40", "4cc51a96df444592ec8276738d4ccd1202e6150d", []question{
		{"How does axum's Router match routes and dispatch to handlers?", []string{"PathRouter", "MethodRouter"}, ""},
		{"How do extractors such as Path, Query and Json work through FromRequest and FromRequestParts?", []string{"from_request_parts|FromRequestParts", "Rejection"}, ""},
		{"How does axum use tower middleware and layers?", []string{"layer(|`layer`|Router::layer", "route_layer|ServiceBuilder|from_fn|Layer<"}, ""},
		{"How is shared application state passed to handlers with State?", []string{"with_state", "FromRef"}, ""},
		{"How does a handler's return value become an HTTP response through IntoResponse?", []string{"IntoResponse", "into_response"}, ""},
	}, 61},
}

// TestKeywords: each question has a word the question does not hold itself,
// and no word has an empty alternative, which every answer would hold. Each
// target has topics, and score has none for another repository.
func TestKeywords(t *testing.T) {
	var names []string
	for _, tg := range targets {
		if names = append(names, tg.name); len(tg.topics()) == 0 {
			t.Errorf("%s has no topics", tg.name)
		}
	}
	if slices.Sort(names); !slices.Equal(names, slices.Sorted(maps.Keys(score.Topics))) {
		t.Errorf("score has topics of %v, the targets are %v", slices.Sorted(maps.Keys(score.Topics)), names)
	}
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
