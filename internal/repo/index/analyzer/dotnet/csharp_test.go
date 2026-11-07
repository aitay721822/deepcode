package dotnet

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/repo/index/analyzer/syntax"
)

// fileScoped has a file-scoped namespace, usings of each kind, a partial
// class with every member kind, records and generics.
const fileScoped = `global using System.Linq;
using static Shop.Core.Guard;
using Money = Shop.Core.Money;
using Shop.Core;

namespace Shop.Ordering;

/// <summary>An order.</summary>
[Serializable]
public sealed partial class Order : Entity, IAggregateRoot
{
    public Order(int id) { Id = id; }
    public int Id { get; private set; }
    public Address Address { get; set; }
    public int this[int i] => i;
    public event EventHandler Changed;
    public event EventHandler<OrderEventArgs> Moved { add {} remove {} }
    public static Order operator +(Order a, Order b) => a;
    public static implicit operator int(Order o) => o.Id;
    /// Ships it.
    [Obsolete]
    public void Ship(Buyer buyer)
    {
        var item = new OrderItem();
        Repo.Save<LineItem>(item);
        buyer.Name = Helper.Format(item);
    }
    private readonly List<OrderItem> _items = new();
    ~Order() {}
}

public record Receipt(int Id);
public record struct Point(int X, int Y);
public interface IRepo<T> where T : Entity { T Get(int id); }
public enum Status { Open, Closed }
public delegate void Handler(Order o);
`

// blockScoped has block namespaces, one nested in another, a nested type,
// a multi-declarator event, operators and a member under #if.
const blockScoped = `using System;
using System.Collections.Generic;

namespace Shop.Billing
{
    // Plain comment.
    public partial class Invoice<TLine> : DocumentBase<TLine>, IComparable<Invoice<TLine>>
        where TLine : ILine, new()
    {
        public class Line { }

        public Invoice(Customer customer) { }

        public TLine this[int index] => default;

        public event EventHandler Paid, Voided;

        public static bool operator ==(Invoice<TLine> a, Invoice<TLine> b) => true;
        public static bool operator !=(Invoice<TLine> a, Invoice<TLine> b) => false;

        public IReadOnlyList<Shop.Core.Tax> Taxes() => Array.Empty<Shop.Core.Tax>();

#if DEBUG
        public void Dump() => Console.WriteLine(typeof(Printer));
#endif
    }

    namespace Export
    {
        internal static class Csv
        {
            public static string Write(Invoice<Line> invoice) => (string)Format.Apply(invoice as Exportable);
        }
    }
}
`

// flat renders symbols as "QualifiedName kind start-end", children indented.
func flat(syms []syntax.Symbol, parent, indent string) []string {
	var out []string
	for _, s := range syms {
		q := s.QualifiedName(parent)
		out = append(out, fmt.Sprintf("%s%s %s %d-%d", indent, q, s.Kind, s.StartLine, s.EndLine))
		out = append(out, flat(s.Children, q, indent+"  ")...)
	}
	return out
}

func TestSymbols(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"file-scoped", fileScoped, []string{
			"Shop.Ordering namespace 6-36",
			"  Shop.Ordering.Order class 8-30",
			"    Shop.Ordering.Order.Order constructor 12-12",
			"    Shop.Ordering.Order.Id property 13-13",
			"    Shop.Ordering.Order.Address property 14-14",
			"    Shop.Ordering.Order.this indexer 15-15",
			"    Shop.Ordering.Order.Changed event 16-16",
			"    Shop.Ordering.Order.Moved event 17-17",
			"    Shop.Ordering.Order.operator + operator 18-18",
			"    Shop.Ordering.Order.operator int operator 19-19",
			"    Shop.Ordering.Order.Ship method 20-27",
			"    Shop.Ordering.Order.~Order method 29-29",
			"  Shop.Ordering.Receipt record 32-32",
			"  Shop.Ordering.Point record 33-33",
			"  Shop.Ordering.IRepo interface 34-34",
			"    Shop.Ordering.IRepo.Get method 34-34",
			"  Shop.Ordering.Status enum 35-35",
			"  Shop.Ordering.Handler delegate 36-36",
		}},
		{"block", blockScoped, []string{
			"Shop.Billing namespace 4-35",
			"  Shop.Billing.Invoice class 6-26",
			"    Shop.Billing.Invoice.Line class 10-10",
			"    Shop.Billing.Invoice.Invoice constructor 12-12",
			"    Shop.Billing.Invoice.this indexer 14-14",
			"    Shop.Billing.Invoice.Paid event 16-16",
			"    Shop.Billing.Invoice.Voided event 16-16",
			"    Shop.Billing.Invoice.operator == operator 18-18",
			"    Shop.Billing.Invoice.operator != operator 19-19",
			"    Shop.Billing.Invoice.Taxes method 21-21",
			"    Shop.Billing.Invoice.Dump method 24-24",
			"  Shop.Billing.Export namespace 28-34",
			"    Shop.Billing.Export.Csv class 30-33",
			"      Shop.Billing.Export.Csv.Write method 32-32",
		}},
		// A doc comment after #region, a record with a base and a body, an
		// explicit interface member holding a local function.
		{"regions", `namespace Shop.Billing
{
    #region Types
    /// <summary>A receipt.</summary>
    [Obsolete("x"), Table(Name = "t")]
    public record Receipt(int Id) : Document(Id), IPrintable
    {
        public Customer? Owner { get; init; }
    }
    #endregion

    public class Service : IService
    {
        void IService.Run() { int Local() => 1; }
    }
}
`, []string{
			"Shop.Billing namespace 1-16",
			"  Shop.Billing.Receipt record 4-9",
			"    Shop.Billing.Receipt.Owner property 8-8",
			"  Shop.Billing.Service class 12-15",
			"    Shop.Billing.Service.Run method 14-14",
		}},
	}
	for _, c := range cases {
		syms, _ := syntax.Parse(CSharp, []byte(c.src))
		if got := flat(syms, "", ""); strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", c.name, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

func TestFacts(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"file-scoped", fileScoped, "global:System.Linq static:Shop.Core.Guard alias:Shop.Core.Money using:Shop.Core ns:Shop.Ordering " +
			"def:Shop.Ordering.Order def:Shop.Ordering.Receipt def:Shop.Ordering.Point def:Shop.Ordering.IRepo def:Shop.Ordering.Status def:Shop.Ordering.Handler " +
			"type:Serializable type:SerializableAttribute type:Entity type:IAggregateRoot type:Address type:EventHandler type:OrderEventArgs " +
			"type:Order type:Obsolete type:ObsoleteAttribute type:Buyer type:OrderItem type:Repo type:LineItem type:Helper type:List type:T"},
		{"block", blockScoped, "using:System using:System.Collections.Generic ns:Shop.Billing ns:Shop.Billing.Export " +
			"def:Shop.Billing.Invoice def:Shop.Billing.Invoice.Line def:Shop.Billing.Export.Csv " +
			"type:DocumentBase type:TLine type:IComparable type:Invoice type:ILine type:Customer type:EventHandler " +
			"type:IReadOnlyList type:Shop.Core.Tax type:Array type:Console type:Printer type:Line type:Format type:Exportable"},
		{"qualified", "using G = global::Shop.Core.Generic<int>;\nclass A { void M() { Shop.Util.Helper.Format(1).Trim(); Settings.Instance.Load(); if (x is Paid or Due) {} } }\n",
			"alias:Shop.Core.Generic def:A type:Shop.Util.Helper type:Settings.Instance type:Paid type:Due"},
		{"using in a namespace", "namespace A { using B.C; namespace D.E { class F { } } }\n", "ns:A using:B.C ns:A.D.E def:A.D.E.F"},
		{"broken", "using ;\nnamespace { class { } }\n", ""},
	}
	for _, c := range cases {
		_, facts := syntax.Parse(CSharp, []byte(c.src))
		if got := strings.Join(facts, " "); got != c.want {
			t.Errorf("%s:\ngot  %s\nwant %s", c.name, got, c.want)
		}
	}
}

// TestMembers: a member's signature is the line naming it, past its
// attributes, and a comment trailing a line is not the next member's.
func TestMembers(t *testing.T) {
	src := "class C\n{\n    [HttpGet(\"x\")]\n    public void M() {}\n    public int A { get; set; } // the a\n    public int B { get; set; }\n" +
		"    [Obsolete]\n    public static C operator +(C a, C b) => a;\n    public event Action E1, E2;\n}\n"
	syms, _ := syntax.Parse(CSharp, []byte(src))
	var got []string
	for _, s := range syms[0].Children {
		got = append(got, fmt.Sprintf("%s %d-%d %s", s.Name, s.StartLine, s.EndLine, s.Signature))
	}
	want := []string{
		"M 3-4 public void M() {}", "A 5-5 public int A { get; set; } // the a", "B 6-6 public int B { get; set; }",
		"operator + 7-8 public static C operator +(C a, C b) => a;",
		"E1 9-9 public event Action E1, E2;", "E2 9-9 public event Action E1, E2;",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A note trailing a file-scoped namespace is not the next type's.
	syms, _ = syntax.Parse(CSharp, []byte("namespace A; // note\nclass B {}\n"))
	if b := syms[0].Children; len(b) != 1 || b[0].StartLine != 2 {
		t.Errorf("file-scoped: %+v", syms)
	}
}

// TestBroken: a namespace with a broken name keeps what it holds and names
// no namespace; a declarator on a line of its own is signed by the type's line.
func TestBroken(t *testing.T) {
	syms, facts := syntax.Parse(CSharp, []byte("namespace { class A {} }\n"))
	if len(syms) != 1 || syms[0].Name != "A" || slices.ContainsFunc(facts, func(f string) bool { return strings.HasPrefix(f, "ns:") }) {
		t.Errorf("broken namespace: %+v %q", syms, facts)
	}
	syms, facts = syntax.Parse(CSharp, []byte("namespace A.\n{\n    class B {}\n}\n"))
	if len(syms) != 1 || syms[0].Name != "B" || slices.ContainsFunc(facts, func(f string) bool { return strings.HasPrefix(f, "ns:") }) {
		t.Errorf("namespace A.: %+v %q", syms, facts)
	}
	syms, _ = syntax.Parse(CSharp, []byte("namespace N;\nclass A { }\n#nullable restore\n")) // a directive takes its line break along
	if len(syms) != 1 || syms[0].EndLine != 3 {
		t.Errorf("file-scoped namespace ending in a directive: %+v", syms)
	}
	syms, _ = syntax.Parse(CSharp, []byte("class C\n{\n    public event Action\n        E1,\n        E2;\n}\n"))
	if e := syms[0].Children; len(e) != 2 || e[0].Signature != "public event Action" {
		t.Errorf("events: %+v", e)
	}
}
