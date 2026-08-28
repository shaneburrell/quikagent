package tui

// theme.go defines the palette and text attributes. The palette is
// GitHub-dark flavored, close to opencode's default theme.

// color is a 24-bit RGB color.
type color struct{ r, g, b uint8 }

var (
	colFg      = color{0xc9, 0xd1, 0xd9}
	colDim     = color{0x8b, 0x94, 0x9e}
	colAccent  = color{0x58, 0xa6, 0xff}
	colGreen   = color{0x3f, 0xb9, 0x50}
	colYellow  = color{0xd2, 0x99, 0x22}
	colRed     = color{0xf8, 0x51, 0x49}
	colCyan    = color{0x39, 0xc5, 0xcf}
	colMagenta = color{0xbc, 0x8c, 0xff}
	colBg      = color{0x0d, 0x11, 0x17}
	colSubtle  = color{0x16, 0x1b, 0x22}
)

// Attr is a set of display attributes for a run of text.
type Attr struct {
	Bold, Dim, Italic bool
	FG, BG            color
	hasFG, hasBG      bool
}

func attr(c color) Attr {
	return Attr{FG: c, hasFG: true}
}

func (a Attr) withBold() Attr   { a.Bold = true; return a }
func (a Attr) withItalic() Attr { a.Italic = true; return a }
func (a Attr) withBG(c color) Attr {
	a.BG = c
	a.hasBG = true
	return a
}

// Commonly used styles.
var (
	styleDefault   = attr(colFg)
	styleDim       = attr(colDim)
	styleAccent    = attr(colAccent)
	styleBold      = attr(colFg).withBold()
	styleGreen     = attr(colGreen)
	styleYellow    = attr(colYellow)
	styleRed       = attr(colRed)
	styleDimItalic = attr(colDim).withItalic()
	styleItalic    = attr(colFg).withItalic()
	styleCode      = attr(colCyan).withBG(colSubtle)
	styleFence     = attr(colDim).withBG(colSubtle)
	styleMagenta   = attr(colMagenta)
	styleNote      = attr(colCyan)
)

// spinnerFrames is the braille spinner.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
