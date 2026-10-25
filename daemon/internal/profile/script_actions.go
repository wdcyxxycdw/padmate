package profile

func checkScriptActions(r *resolver, p *Profile) {
	if p.Script != nil {
		return
	}
	layers := append([]*Layer(nil), p.Layers...)
	if p.Common != nil {
		layers = append(layers, p.Common)
	}
	layers = append(layers, p.FunctionSets()...)
	for _, l := range layers {
		for _, binding := range l.Bindings {
			for g := GesturePress; g <= GestureHold; g++ {
				a := binding.Action(g)
				if a != nil && a.Type == ActionScript {
					n := r.root.lookupPointer(a.ID)
					r.errorAt(n.start, a.ID, "script action requires the profile's script configuration")
				}
			}
		}
	}
}
