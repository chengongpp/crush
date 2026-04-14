package main

import (
	"fmt"
	"math/rand/v2"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/logo"
	"github.com/charmbracelet/x/exp/slice"
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
	fmt.Println(renderLetterforms(false))
	for range 10 {
		fmt.Println(renderLetterforms(true))
	}
}
