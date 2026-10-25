package script

import (
	"os"
	"regexp"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

func TestMathSurfaceMatchesFilteredDeclarations(t *testing.T) {
	source, err := os.ReadFile("dtsgen/padmate.d.ts")
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := dtsgen.Filter(source, dtsgen.HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	declarations := declarationBody(t, stripDeclarationComments(string(filtered)), `declare module "padmate/math" {`)
	dir := t.TempDir()
	if err := writeScript(dir, "main.ts", `import * as math from "padmate/math"; globalThis.mathProbe = math;`); err != nil {
		t.Fatal(err)
	}
	bundle, err := compile.Build(compile.BuildOptions{Root: dir, Entry: "main.ts", API: dtsgen.HostAPI.String()})
	if err != nil {
		t.Fatal(err)
	}
	program, err := goja.Compile("math.js", string(bundle.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(clock.NewFake())
	defer func() { _ = h.Dispose() }()
	if err := h.Activate(program); err != nil {
		t.Fatal(err)
	}
	module := h.VM.Get("mathProbe").ToObject(h.VM)
	values := regexp.MustCompile(`export (function|class) (\w+)`).FindAllStringSubmatch(declarations, -1)
	var names []string
	for _, value := range values {
		name := value[2]
		names = append(names, name)
		if value[1] == "function" {
			assertArity(t, name, module.Get(name), exportedFunctionArity(t, declarations, name))
			continue
		}
		members := declarationMembers(t, declarationBody(t, declarations, "export class "+name+" {"))
		constructor := module.Get(name).ToObject(h.VM)
		arity := 0
		if n, ok := members["constructor"]; ok {
			arity = n
			delete(members, "constructor")
		}
		assertArity(t, name, constructor, arity)
		prototype := constructor.Get("prototype").ToObject(h.VM)
		for member, n := range members {
			assertArity(t, name+"."+member, prototype.Get(member), n)
		}
		ownNames, err := h.VM.RunString(`Object.getOwnPropertyNames(mathProbe.` + name + `.prototype).filter(k => k !== "constructor")`)
		if err != nil {
			t.Fatal(err)
		}
		if got := ownNames.ToObject(h.VM).Get("length").ToInteger(); got != int64(len(members)) {
			t.Errorf("%s prototype has %d members; declarations have %d", name, got, len(members))
		}
	}
	assertKeys(t, "padmate/math", module, names)
}
