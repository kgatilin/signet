package main

import (
	"os"
)

var colorEnabled bool

const (
	colorReset  = "\x1b[0m"
	colorBold   = "1"
	colorDim    = "2"
	colorRed    = "31"
	colorGreen  = "32"
	colorYellow = "33"
	colorCyan   = "36"
)

func configureColor(stdout *os.File) {
	colorEnabled = shouldUseColor(stdout)
}

func shouldUseColor(stdout *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func color(code, text string) string {
	if !colorEnabled {
		return text
	}
	return "\x1b[" + code + "m" + text + colorReset
}

func bold(text string) string {
	return color(colorBold, text)
}

func dim(text string) string {
	return color(colorDim, text)
}

func cyan(text string) string {
	return color(colorCyan, text)
}

func green(text string) string {
	return color(colorGreen, text)
}

func yellow(text string) string {
	return color(colorYellow, text)
}

func red(text string) string {
	return color(colorRed, text)
}
