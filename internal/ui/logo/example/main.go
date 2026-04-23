package main

import (
	"fmt"
	"math/rand/v2"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/logo"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/exp/slice"
	"github.com/charmbracelet/x/term"
)

func renderLetterforms(stretch bool) string {
	letterFuncs := []func(bool) string{
		logo.LetterH,
		logo.LetterY,
		logo.LetterYAlt,
		logo.LetterP,
		logo.LetterE,
		logo.LetterEAlt,
		logo.LetterR,
		logo.LetterC,
		logo.LetterR,
		logo.LetterU,
		logo.LetterSAlt,
		logo.LetterH,
	}

	// Which letter to stretch, if we're stretching.
	stretchIndex := -1
	if stretch {
		stretchIndex = rand.IntN(len(letterFuncs))
	}

	// Build letterforms.
	letterforms := make([]string, len(letterFuncs))
	for i, f := range letterFuncs {
		letterforms[i] = f(stretch && i == stretchIndex)
	}
	letterforms = slice.Intersperse(letterforms, " ")

	return lipgloss.JoinHorizontal(lipgloss.Top, letterforms...)
}

func main() {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not get terminal size: %s", err)
	}

	s := styles.DefaultStyles()
	opts := logo.Opts{
		FieldColor:   s.LogoFieldColor,
		TitleColorA:  s.LogoTitleColorA,
		TitleColorB:  s.LogoTitleColorB,
		CharmColor:   s.LogoCharmColor,
		VersionColor: s.LogoVersionColor,
		Width:        w,
	}

	lipgloss.Println(logo.Render(s.Base, "v1.0.0", false, opts))
	lipgloss.Println(logo.Render(s.Base, "v1.0.0", true, opts))

	fmt.Println(renderLetterforms(false))
	for range 5 {
		fmt.Println(renderLetterforms(true))
	}
}
