// Package ui provides the embedded web UI for pipepie.
package ui

import "embed"

//go:embed static/*
var Static embed.FS
