package dotnet

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// files are the facts of a solution's C# files: an SDK-style shop, a .NET
// Framework app, and an F# tool.
var files = map[string][]string{
	"src/Shop.Core/Money.cs":    {"ns:Shop.Core", "def:Shop.Core.Money"},
	"src/Shop.Core/Guard.cs":    {"ns:Shop.Core", "def:Shop.Core.Guard"},
	"src/Shop.Core/Entity.cs":   {"ns:Shop", "def:Shop.Entity"},
	"src/Shop.Core/Settings.cs": {"ns:Shop.Core", "def:Shop.Core.Settings"},

	"src/Shop.Ordering/GlobalUsings.cs": {"global:Shop.Shared"},
	// Entity from a parent namespace, Customer from its own (not Shop.Shared's),
	// Money by a using, Clock by the global using, Tax by the project's <Using>,
	// Settings through a property, Order's other partial file.
	"src/Shop.Ordering/Order.cs": {"using:Shop.Core", "ns:Shop.Ordering", "def:Shop.Ordering.Order",
		"type:Entity", "type:Customer", "type:Money", "type:Clock", "type:Tax", "type:Settings.Instance", "type:Order", "type:T"},
	"src/Shop.Ordering/Order.Lines.cs":  {"ns:Shop.Ordering", "def:Shop.Ordering.Order"},
	"src/Shop.Ordering/Customer.cs":     {"static:Shop.Core.Guard", "alias:Shop.Core.Money", "ns:Shop.Ordering", "def:Shop.Ordering.Customer"},
	"src/Shop.Ordering/Web/Checkout.cs": {"ns:Shop.Ordering.Web", "def:Shop.Ordering.Web.Checkout", "type:Order"},

	"src/Shop.Shared/Clock.cs":    {"ns:Shop.Shared", "def:Shop.Shared.Clock"},
	"src/Shop.Shared/Customer.cs": {"ns:Shop.Shared", "def:Shop.Shared.Customer"},
	"src/Shop.Shared/Tax.cs":      {"ns:Shop.Pricing", "def:Shop.Pricing.Tax"},

	// Another project: Shop.Ordering's global using and <Using> are not its.
	"src/Shop.Jobs/Job.cs": {"ns:Shop.Jobs", "def:Shop.Jobs.Job", "type:Clock", "type:Tax"},

	"legacy/Legacy.App/Program.cs":     {"using:Legacy.Core", "ns:Legacy.App", "def:Legacy.App.Program", "type:Repository", "type:Text"},
	"legacy/Legacy.Core/Repository.cs": {"ns:Legacy.Core", "def:Legacy.Core.Repository"},
	"legacy/Legacy.Util/Text.cs":       {"ns:Legacy.Util", "def:Legacy.Util.Text"},
}

var configs = map[string]string{
	"src/Shop.Core/Shop.Core.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
	"src/Shop.Ordering/Shop.Ordering.csproj": `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="..\Shop.Core\Shop.Core.csproj" />
    <ProjectReference Include="..\shop.shared\SHOP.SHARED.csproj" />
    <ProjectReference Include="..\..\..\outside\Other.csproj" />
    <PackageReference Include="Serilog" Version="3.1.1" />
    <Using Include="Shop.Pricing" />
    <Using Include="Shop.Core.Guard" Static="true" />
  </ItemGroup>
</Project>`,
	"src/Shop.Shared/Shop.Shared.csproj": `<Project Sdk="Microsoft.NET.Sdk" />`,
	"src/Shop.Jobs/Shop.Jobs.csproj":     `<Project Sdk="Microsoft.NET.Sdk.Worker" />`,
	"tools/Tool.fsproj":                  `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><ProjectReference Include="..\src\Shop.Core\Shop.Core.csproj" /></ItemGroup></Project>`,

	"legacy/Directory.Build.props": `<Project><ItemGroup><Using Include="Legacy.Util" /></ItemGroup></Project>`,
	"legacy/Legacy.App/Legacy.App.csproj": "\ufeff" + `<?xml version="1.0" encoding="utf-8"?>
<Project ToolsVersion="15.0" DefaultTargets="Build" xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <Import Project="$(MSBuildExtensionsPath)\$(MSBuildToolsVersion)\Microsoft.Common.props" Condition="Exists('$(MSBuildExtensionsPath)\$(MSBuildToolsVersion)\Microsoft.Common.props')" />
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <RootNamespace>Legacy.App</RootNamespace>
    <TargetFrameworkVersion>v4.7.2</TargetFrameworkVersion>
  </PropertyGroup>
  <ItemGroup>
    <Reference Include="System" />
    <Reference Include="Newtonsoft.Json, Version=13.0.0.0, Culture=neutral">
      <HintPath>..\packages\Newtonsoft.Json.13.0.1\lib\net45\Newtonsoft.Json.dll</HintPath>
    </Reference>
  </ItemGroup>
  <ItemGroup>
    <Compile Include="Program.cs" />
    <Compile Include="Properties\AssemblyInfo.cs" />
  </ItemGroup>
  <ItemGroup>
    <None Include="packages.config" />
  </ItemGroup>
  <ItemGroup>
    <ProjectReference Include="..\Legacy.Core\Legacy.Core.csproj">
      <Project>{0e5b3b5e-1111-2222-3333-444455556666}</Project>
      <Name>Legacy.Core</Name>
    </ProjectReference>
  </ItemGroup>
  <Import Project="$(MSBuildToolsPath)\Microsoft.CSharp.targets" />
</Project>`,
	"legacy/Legacy.App/packages.config": `<?xml version="1.0" encoding="utf-8"?>
<packages>
  <package id="Newtonsoft.Json" version="13.0.1" targetFramework="net472" />
</packages>`,
	"legacy/Legacy.Core/Legacy.Core.csproj": `<?xml version="1.0" encoding="utf-8"?>
<Project ToolsVersion="4.0" xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <ItemGroup><Compile Include="Repository.cs" /></ItemGroup>
</Project>`,
}

func TestResolve(t *testing.T) {
	want := []string{
		"legacy/Legacy.App/Legacy.App.csproj legacy/Legacy.Core/Legacy.Core.csproj",
		"legacy/Legacy.App/Program.cs legacy/Legacy.Core/Repository.cs",
		"legacy/Legacy.App/Program.cs legacy/Legacy.Util/Text.cs",
		"src/Shop.Ordering/Customer.cs src/Shop.Core/Guard.cs",
		"src/Shop.Ordering/Customer.cs src/Shop.Core/Money.cs",
		"src/Shop.Ordering/Order.cs src/Shop.Core/Entity.cs",
		"src/Shop.Ordering/Order.cs src/Shop.Core/Money.cs",
		"src/Shop.Ordering/Order.cs src/Shop.Core/Settings.cs",
		"src/Shop.Ordering/Order.cs src/Shop.Ordering/Customer.cs",
		"src/Shop.Ordering/Order.cs src/Shop.Ordering/Order.Lines.cs",
		"src/Shop.Ordering/Order.cs src/Shop.Shared/Clock.cs",
		"src/Shop.Ordering/Order.cs src/Shop.Shared/Tax.cs",
		"src/Shop.Ordering/Shop.Ordering.csproj src/Shop.Core/Shop.Core.csproj",
		"src/Shop.Ordering/Shop.Ordering.csproj src/Shop.Shared/Shop.Shared.csproj",
		"src/Shop.Ordering/Web/Checkout.cs src/Shop.Ordering/Order.Lines.cs",
		"src/Shop.Ordering/Web/Checkout.cs src/Shop.Ordering/Order.cs",
		"tools/Tool.fsproj src/Shop.Core/Shop.Core.csproj",
	}
	if got := render(Resolve(files, configs)); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestCap: a file naming more types than maxEdges keeps the first it names.
func TestCap(t *testing.T) {
	files := map[string][]string{}
	big := []string{"ns:App"}
	for i := range maxEdges + 10 {
		files[fmt.Sprintf("App/T%02d.cs", i)] = []string{"ns:App", fmt.Sprintf("def:App.T%02d", i)}
		big = append(big, fmt.Sprintf("type:T%02d", i))
	}
	files["App/Big.cs"] = big
	got := render(Resolve(files, nil))
	if len(got) != maxEdges || got[0] != "App/Big.cs App/T00.cs" || got[maxEdges-1] != fmt.Sprintf("App/Big.cs App/T%02d.cs", maxEdges-1) {
		t.Errorf("got %d edges: %v", len(got), got)
	}
}

// render sorts edges as "src dst" lines, each once, as the caller keeps them.
func render(edges [][2]string) []string {
	slices.SortFunc(edges, func(a, b [2]string) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
	var out []string
	for _, e := range slices.Compact(edges) {
		out = append(out, e[0]+" "+e[1])
	}
	return out
}
