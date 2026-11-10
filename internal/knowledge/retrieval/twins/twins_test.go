package twins

import (
	"slices"
	"testing"
)

// TestBases: a declaration's bases are what follows C#'s ":" or Java's
// extends, each by its last name, type arguments, parameters and attributes
// aside; a type parameter's bound, a where clause and Java's interfaces are
// no base.
func TestBases(t *testing.T) {
	for sig, want := range map[string][]string{
		"public record OrderPaid(int OrderId, string Buyer) : IntegrationEvent;":  {"IntegrationEvent"},
		"public record OrderPaid(int OrderId) : Events.IntegrationEvent(OrderId)": {"IntegrationEvent"},
		"public class Repo<T> : Base<T, int>, IRepo<T> where T : Entity {":        {"Base", "IRepo"},
		"public class Handler<T> where T : Entity, new() {":                       nil,
		"[Serializable] public sealed class Paid : IntegrationEvent":              {"IntegrationEvent"},
		"public class Paid {": nil,
		"public class Box<T extends Comparable<T>> extends Base {":                      {"Base"},
		"public class Paid extends com.acme.Event implements Serializable, Cloneable {": {"Event"},
		"interface Handler extends Base, Other {":                                       {"Base", "Other"},
		"type Paid struct {": nil,
	} {
		if got := Bases(sig); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", sig, got, want)
		}
	}
	if got := project("src/Ordering/Events/Paid.cs", []string{"src/Ordering/Events/x.txt", "src/Ordering/Ordering.csproj", "eShop.sln", "src/Order/Order.csproj"}); got != "src/Ordering/Events" {
		t.Errorf("project %q", got) // the nearest folder of any file given, the longest
	}
}
