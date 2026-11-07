package bench

// target is a benchmark repository: DeepWiki and Code Wiki both have its wiki.
// It is indexed at base, then updated to head (one small commit later), where
// the questions are asked and the retrieval eval runs (its corpus pins head).
type target struct {
	name, url, base, head string
	questions             []string
}

var targets = []target{
	{"chi", "https://github.com/go-chi/chi", "04f7f55a7b5ddb2ecd79a8d813b2256c1113b8a9", "9f8adb1aabaa8a91180a821111d3f8e668d1c0bf", []string{
		"How does chi's router match a request path to a handler, including URL parameters?",
		"How do middlewares work in chi, and how are they chained with Use, With and Group?",
		"How does chi mount a sub-router, and how does the routing context reach it?",
		"Which middlewares does chi ship, and how does the Timeout middleware work?",
		"How can I get the matched route pattern of a request, for example for metrics?",
	}},
	{"zustand", "https://github.com/pmndrs/zustand", "b4883239492c687244d92cf50bf4fae7125ae5ad", "382d29c485e67018c06e75ec7235130d0968522c", []string{
		"How does create build a store and its React hook from a state creator?",
		"How does setState merge an update and notify the subscribers?",
		"How does the persist middleware save the state and rehydrate it?",
		"How does zustand avoid needless re-renders with selectors and useShallow?",
		"How do the devtools and immer middlewares wrap set?",
	}},
	{"express", "https://github.com/expressjs/express", "6389ae5e397acf28dd66765fbcd9c487f7a61e17", "43fca52d28a422c8be48fd489cbd4f2d6152bab6", []string{
		"How does Express route a request through app.use and app.get to its handler?",
		"How does the router handle route parameters and app.param?",
		"How does Express render a view with a template engine?",
		"How are errors passed to error-handling middleware?",
		"What does res.send do with strings, Buffers and objects?",
	}},
	{"eshop", "https://github.com/dotnet/eShop", "dc7ea499cd356924fb6689b3702964a5869dbae9", "913acea66708a59595351174cee9b29f37179d29", []string{
		"Which services make up eShop, and how does .NET Aspire orchestrate them?",
		"How does the Ordering service take an order from basket checkout to payment?",
		"How do the services communicate through integration events and the event bus?",
		"How is the catalog's semantic search with AI embeddings implemented?",
		"How are identity and authentication handled across the web app and the APIs?",
	}},
	{"petclinic", "https://github.com/spring-projects/spring-petclinic", "0d064b04958da7715a9138459bafbde3bd10949d", "500158f732419217507c7656904b8e6aa1bcc0d6", []string{
		"How is the domain (owners, pets, visits, vets) modelled and persisted with JPA?",
		"How does OwnerController find, create and edit owners?",
		"How are the vets served both as an HTML page and as JSON or XML?",
		"How is caching configured for the list of vets?",
		"How does the application validate a new pet's form?",
	}},
	{"gson", "https://github.com/google/gson", "57731049da3e415be3d5d34db1ca3b80f3c73810", "65df6e5a99747f73d386f3a61d887a0257255e0f", []string{
		"How does Gson serialize an object to JSON by reflection?",
		"How do TypeAdapter and TypeAdapterFactory work, and how is one chosen for a type?",
		"How does JsonReader parse JSON as a stream?",
		"How does Gson handle generic types with TypeToken?",
		"How do @SerializedName, @Expose and FieldNamingPolicy control field names?",
	}},
	{"axum", "https://github.com/tokio-rs/axum", "40d061fdacd468217c7a609bede16357276b6c40", "4cc51a96df444592ec8276738d4ccd1202e6150d", []string{
		"How does axum's Router match routes and dispatch to handlers?",
		"How do extractors such as Path, Query and Json work through FromRequest and FromRequestParts?",
		"How does axum use tower middleware and layers?",
		"How is shared application state passed to handlers with State?",
		"How does a handler's return value become an HTTP response through IntoResponse?",
	}},
}
