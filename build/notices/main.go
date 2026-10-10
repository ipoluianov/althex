// Command notices writes app/notices.txt: the licences AltHex is
// distributed under and the notices of what is built into it, which the
// licences of those parts ask to be reproduced with the binaries (the Go
// runtime and modules, the register descriptions of the peripheral
// database). Run it after changing the dependencies:
//
//	go run ./build/notices
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// The platforms of the releases: each links its own modules
var platforms = []string{"linux/amd64", "linux/arm64", "windows/amd64", "darwin/arm64"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run() error {
	// module path -> directory, for the modules linked on any platform
	mods := map[string]string{}
	for _, p := range platforms {
		goos, goarch, _ := strings.Cut(p, "/")
		cmd := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Dir}}{{end}}{{end}}", ".")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("go list for %s: %v", p, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if path, dir, ok := strings.Cut(line, "\t"); ok {
				mods[path] = dir
			}
		}
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return err
	}

	var b bytes.Buffer
	section := func(title, text string) {
		fmt.Fprintf(&b, "%s\n%s\n\n%s\n\n", strings.Repeat("=", 78), title, strings.TrimSpace(text))
	}
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "notices: missing", path)
			return ""
		}
		return string(data)
	}
	b.WriteString("AltHex and the software built into it, with their licences.\n\n")
	section("AltHex", read("LICENSE"))
	section("The Go programming language (runtime and standard library)", read(filepath.Join(strings.TrimSpace(string(goroot)), "LICENSE")))

	paths := make([]string, 0, len(mods))
	for p := range mods {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	apache := ""
	for _, p := range paths {
		texts := licenceFiles(mods[p])
		if len(texts) == 0 {
			fmt.Fprintln(os.Stderr, "notices: no licence file in", p)
			continue
		}
		section(p, strings.Join(texts, "\n\n"))
		for _, t := range texts {
			if apache == "" && strings.Contains(t, "Apache License") && strings.Contains(t, "TERMS AND CONDITIONS") {
				apache = t
			}
		}
	}

	section("Peripheral register database (core/periph): sources",
		"Made of CMSIS-SVD files of Nordic, Raspberry Pi, Microchip, NXP, Freescale, Texas Instruments, "+
			"Infineon, Espressif, GigaDevice, SiFive, WCH, STMicroelectronics and of Microchip ATDF files; "+
			"the list of sources and their licences is core/periph/data/SOURCES.md of the source code. "+
			"Parts under the Apache License 2.0 (Microchip ATDF and SVD, Espressif, STMicroelectronics, "+
			"GigaDevice) are used under its terms, given below.")
	section("Peripheral register database: notices of the sources", read("core/periph/data/NOTICES.txt"))
	if apache != "" {
		section("Apache License 2.0", apache)
	} else {
		return fmt.Errorf("no copy of the Apache License 2.0 found among the modules")
	}
	return os.WriteFile("app/notices.txt", b.Bytes(), 0o644)
}

// licenceFiles reads the licence and notice files at the root of a module
func licenceFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := strings.ToUpper(e.Name())
		if e.IsDir() || !(strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "LICENCE") ||
			strings.HasPrefix(n, "COPYING") || strings.HasPrefix(n, "NOTICE") || strings.HasPrefix(n, "COPYRIGHT")) {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, strings.TrimSpace(string(data)))
		}
	}
	return out
}
