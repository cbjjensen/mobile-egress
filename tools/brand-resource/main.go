// Command brand-resource embeds the generated ICO in the Windows Client and
// installer. It is a build-time tool; no dependency is added to the applications.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tc-hib/winres"
)

func main() {
	iconPath := flag.String("icon", "", "input ICO path")
	outputPath := flag.String("output", "", "output AMD64 COFF resource path")
	flag.Parse()
	if err := generate(*iconPath, *outputPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(iconPath, outputPath string) error {
	input, err := os.Open(iconPath)
	if err != nil {
		return err
	}
	defer input.Close()
	icon, err := winres.LoadICO(input)
	if err != nil {
		return err
	}
	resources := winres.ResourceSet{}
	// Wails v2 loads application icon resource 3 for its native window/taskbar.
	if err := resources.SetIcon(winres.ID(3), icon); err != nil {
		return err
	}
	output, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	if err := resources.WriteObject(output, winres.ArchAMD64); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
