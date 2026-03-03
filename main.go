package main

import (
	"github.com/manujgrover71/goslicecheck/analyser"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(analyser.Analyzer)
}
