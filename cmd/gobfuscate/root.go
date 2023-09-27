package cmd

import (
	"crypto/rand"
	"flag"
	"fmt"
	"go/build"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/sonnt85/gobfuscate"
	"github.com/sonnt85/gosystem"
	"github.com/spf13/cobra"
)

// Command line arguments.
var (
	customPadding       string
	tags                string
	outputGopath        bool
	keepTests           bool
	winHide             bool
	ldf                 string
	go11module          string
	noStaticLink        bool
	preservePackageName bool
	verbose             bool
	ignoreDelTmp        bool
)

var (
	BuildCmd = &cobra.Command{
		Use:     "build pkgName outPath",
		Aliases: []string{"b"},
		Short:   "Build go project",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 2 {
				fmt.Fprintln(os.Stderr, "missing 2 parameters pkg_name out_path")
				flag.PrintDefaults()
				os.Exit(1)
			}
			if len(os.Getenv("GO111MODULE")) == 0 {
				os.Setenv("GO111MODULE", go11module)
			}

			pkgName := args[0]
			outPath := args[1]
			// os.Setenv("GO111MODULE", "off")
			if !obfuscate(pkgName, outPath) {
				os.Exit(1)
			}
		},
	}
)

func Execute() {
	Init()
	BuildCmd.Execute()
}

func Init() {
	BuildCmd.Flags().StringVarP(&customPadding, "padding", "p", "", "use a custom padding for hashing sensitive information (otherwise a random padding will be used)")
	BuildCmd.Flags().BoolVarP(&outputGopath, "nobuild", "n", false, "only copy source code, GOPATH to new dir then exit, need manual build")
	BuildCmd.Flags().BoolVarP(&keepTests, "keeptests", "k", false, "keep _test.go files")
	BuildCmd.Flags().BoolVarP(&winHide, "winhide", "w", false, "hide windows GUI")
	BuildCmd.Flags().BoolVarP(&noStaticLink, "nostatic", "s", false, "do not statically link")
	BuildCmd.Flags().BoolVarP(&preservePackageName, "noencrypt", "e", false,
		"no encrypted package name for go build command (works when main package has CGO code)")
	BuildCmd.Flags().BoolVarP(&verbose, "verbose", "v", true, "verbose mode")
	BuildCmd.Flags().BoolVarP(&ignoreDelTmp, "cleanup", "c", false, "no cleanup when exit")
	BuildCmd.Flags().StringVarP(&tags, "tags", "t", "", "tags are passed to the go compiler")
	BuildCmd.Flags().StringVar(&ldf, "ldf", "", "more ldflag when build")
	BuildCmd.Flags().StringVar(&go11module, "go11module", "auto", "env go11module")
}

func obfuscate(pkgName, outPath string) bool {
	var newGopath string
	if outputGopath {
		newGopath = outPath
		if err := os.Mkdir(newGopath, 0755); err != nil {
			fmt.Fprintln(os.Stderr, "Failed to create destination:", err)
			return false
		}
	} else {
		var err error
		newGopath, err = os.MkdirTemp("", "")
		if err != nil {
			fmt.Fprintln(os.Stderr, "Failed to create temp dir:", err)
			return false
		}
		if !ignoreDelTmp {
			gosystem.InitSignal(func(s os.Signal) int {
				os.RemoveAll(newGopath)
				return 0
			})
		}
	}
	log.Printf("Origin GOPATH: %s\nGO111MODULE: %s", os.Getenv("GOPATH"), os.Getenv("GO111MODULE"))

	log.Printf("Copying to new GOPATH %s...\n", newGopath)

	if err := gobfuscate.CopyGopath(pkgName, newGopath, keepTests); err != nil {
		moreInfo := "\nNote: Setting GO111MODULE env variable to `off` may resolve the above error."
		if os.Getenv("GO111MODULE") == "off" {
			moreInfo = ""
		}
		fmt.Fprintln(os.Stderr, "Failed to copy into a new GOPATH:", err, moreInfo)
		return false
	}
	var n gobfuscate.NameHasher
	if customPadding == "" {
		buf := make([]byte, 32)
		rand.Read(buf)
		n = buf
	} else {
		n = []byte(customPadding)
	}

	log.Println("Obfuscating package names...")
	if err := gobfuscate.ObfuscatePackageNames(newGopath, n); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to obfuscate package names:", err)
		return false
	}
	log.Println("Obfuscating strings...")
	if err := gobfuscate.ObfuscateStrings(newGopath); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to obfuscate strings:", err)
		return false
	}
	log.Println("Obfuscating symbols...")
	if err := gobfuscate.ObfuscateSymbols(newGopath, n); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to obfuscate symbols:", err)
		return false
	}

	if outputGopath {
		return true
	}

	ctx := build.Default

	newPkg := pkgName
	if !preservePackageName {
		newPkg = encryptComponents(pkgName, n)
	}

	ldflags := `-s -w`
	if winHide {
		ldflags += " -H=windowsgui"
	}
	if !noStaticLink {
		ldflags += ` -extldflags '-static'`
	}
	if len(ldf) != 0 {
		ldflags += " " + ldf
	}

	goCache := newGopath + "/cache"
	os.Mkdir(goCache, 0755)

	arguments := []string{"build", "-trimpath", "-ldflags", ldflags, "-tags", tags, "-o", outPath, newPkg}
	environment := []string{
		"GO111MODULE=off", // needs to be off to make Go search GOPATH
		"GOROOT=" + ctx.GOROOT,
		"GOARCH=" + ctx.GOARCH,
		"GOOS=" + ctx.GOOS,
		"GOPATH=" + newGopath,
		"PATH=" + os.Getenv("PATH"),
		"GOCACHE=" + goCache,
	}

	cmd := exec.Command("go", arguments...)
	cmd.Env = environment
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if verbose {
		fmt.Println()
		fmt.Println("[Verbose] Temporary path:", newGopath)
		fmt.Println("[Verbose] Go build command: go", strings.Join(arguments, " "))
		fmt.Println("[Verbose] Environment variables:")
		for _, envLine := range environment {
			fmt.Println(envLine)
		}
		fmt.Println()
	}

	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to compile:", err)
		return false
	}

	return true
}

func encryptComponents(pkgName string, n gobfuscate.NameHasher) string {
	comps := strings.Split(pkgName, "/")
	for i, comp := range comps {
		comps[i] = n.Hash(comp)
	}
	return strings.Join(comps, "/")
}
