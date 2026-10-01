package scriptlayout

// builtins lists Unity types whose serialized form is defined by native
// code rather than by their managed fields, as of Unity 2020.3.
var builtins = map[string]func() *Node{
	"UnityEngine.Vector2":    func() *Node { return floats("x", "y") },
	"UnityEngine.Vector3":    func() *Node { return floats("x", "y", "z") },
	"UnityEngine.Vector4":    func() *Node { return floats("x", "y", "z", "w") },
	"UnityEngine.Quaternion": func() *Node { return floats("x", "y", "z", "w") },
	"UnityEngine.Color":      func() *Node { return floats("r", "g", "b", "a") },
	"UnityEngine.Rect":       func() *Node { return floats("x", "y", "width", "height") },
	"UnityEngine.Vector2Int": func() *Node { return ints("x", "y") },
	"UnityEngine.Vector3Int": func() *Node { return ints("x", "y", "z") },
	"UnityEngine.RectInt":    func() *Node { return ints("x", "y", "width", "height") },
	"UnityEngine.RectOffset": rectOffset,
	"UnityEngine.Color32":    func() *Node { return strct(prim("rgba", 4)) },
	"UnityEngine.LayerMask":  func() *Node { return strct(prim("m_Bits", 4)) },
	"UnityEngine.Bounds": func() *Node {
		return strct(named("m_Center", floats("x", "y", "z")), named("m_Extent", floats("x", "y", "z")))
	},
	"UnityEngine.BoundsInt": func() *Node {
		return strct(named("m_Position", ints("x", "y", "z")), named("m_Size", ints("x", "y", "z")))
	},
	"UnityEngine.Matrix4x4":                      matrix,
	"UnityEngine.Hash128":                        func() *Node { return strct(prim("bytes", 16)) },
	"UnityEngine.Rendering.SphericalHarmonicsL2": func() *Node { return strct(prim("sh", 27*4)) },
	"UnityEngine.PropertyName":                   func() *Node { return &Node{Kind: KindString, Align: true} },
	"UnityEngine.AnimationCurve":                 animationCurve,
	"UnityEngine.Gradient":                       gradient,
	"UnityEngine.GUIStyle":                       guiStyle,
	"UnityEngine.ExposedReference`1": func() *Node {
		return strct(&Node{Name: "exposedName", Kind: KindString, Align: true}, prim("defaultValue", pptrSize))
	},
	"UnityEngine.LazyLoadReference`1": func() *Node { return strct(prim("m_asset", pptrSize)) },
}

func prim(name string, size int) *Node {
	return &Node{Name: name, Kind: KindPrimitive, Size: size}
}

func strct(fields ...*Node) *Node {
	return &Node{Kind: KindStruct, Fields: fields}
}

func named(name string, n *Node) *Node {
	n.Name = name
	return n
}

func floats(names ...string) *Node {
	n := strct()
	for _, name := range names {
		n.Fields = append(n.Fields, prim(name, 4))
	}
	return n
}

func ints(names ...string) *Node { return floats(names...) }

func rectOffset() *Node { return ints("m_Left", "m_Right", "m_Top", "m_Bottom") }

func matrix() *Node {
	n := strct()
	for col := range 4 {
		for row := range 4 {
			n.Fields = append(n.Fields, prim("e"+string(rune('0'+row))+string(rune('0'+col)), 4))
		}
	}
	return n
}

func animationCurve() *Node {
	key := floats("time", "value", "inSlope", "outSlope", "weightedMode", "inWeight", "outWeight")
	key.Name = "data"
	return strct(
		&Node{Name: "m_Curve", Kind: KindArray, Elem: key, Align: true},
		prim("m_PreInfinity", 4),
		prim("m_PostInfinity", 4),
		prim("m_RotationOrder", 4),
	)
}

func gradient() *Node {
	n := strct()
	for i := range 8 {
		n.Fields = append(n.Fields, named("key"+string(rune('0'+i)), floats("r", "g", "b", "a")))
	}
	for _, prefix := range []string{"ctime", "atime"} {
		for i := range 8 {
			n.Fields = append(n.Fields, prim(prefix+string(rune('0'+i)), 2))
		}
	}
	alpha := prim("m_NumAlphaKeys", 1)
	alpha.Align = true
	n.Fields = append(n.Fields, prim("m_Mode", 4), prim("m_NumColorKeys", 1), alpha)
	return n
}

func guiStyle() *Node {
	n := strct(&Node{Name: "m_Name", Kind: KindString, Align: true})
	for _, s := range []string{"m_Normal", "m_Hover", "m_Active", "m_Focused", "m_OnNormal", "m_OnHover", "m_OnActive", "m_OnFocused"} {
		n.Fields = append(n.Fields, named(s, strct(
			prim("m_Background", pptrSize),
			&Node{Name: "m_ScaledBackgrounds", Kind: KindArray, Elem: prim("data", pptrSize), Align: true},
			named("m_TextColor", floats("r", "g", "b", "a")),
		)))
	}
	for _, s := range []string{"m_Border", "m_Margin", "m_Padding", "m_Overflow"} {
		n.Fields = append(n.Fields, named(s, rectOffset()))
	}
	richText := prim("m_RichText", 1)
	richText.Align = true
	stretchHeight := prim("m_StretchHeight", 1)
	stretchHeight.Align = true
	n.Fields = append(n.Fields,
		prim("m_Font", pptrSize), prim("m_FontSize", 4), prim("m_FontStyle", 4), prim("m_Alignment", 4),
		prim("m_WordWrap", 1), richText,
		prim("m_TextClipping", 4), prim("m_ImagePosition", 4),
		named("m_ContentOffset", floats("x", "y")),
		prim("m_FixedWidth", 4), prim("m_FixedHeight", 4),
		prim("m_StretchWidth", 1), stretchHeight,
	)
	return n
}
