package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
)

var (
	kube_go_package string
	k8sm_go_package string
	k8_cmd          []string
	tempDirs        []string
	cmd_dirs        []string
	lib_dirs        []string
	target          = "./target"
)

func dieIf(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func run(dir, command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.Dir = dir

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return err
	}
	return nil
}

// a walk path function to search for all *.go files
func pathForGoExt(path string, f os.FileInfo, err error) error {
	match, err := filepath.Match("*.go", f.Name())
	dieIf(err)
	if match {
		// adding the found results to a temporary slice
		tempDirs = append(tempDirs, path)
	}
	return nil
}

func createSet(slice []string) []string {
	seen := make(map[string]bool)
	set := []string{}
	for _, v := range slice {
		if seen[v] {
			break
		}
		seen[v] = true
		set = append(set, v)
	}
	return set

}

func all(current_path string) {

	// constructing CMD_DIRS
	err := filepath.Walk(path.Join(current_path, "cmd"), pathForGoExt)
	dieIf(err)

	cmd_dirs = createSet(tempDirs)

	// clearing tempDirs to reuse
	tempDirs = tempDirs[:0]

	// constructing LIB_DIRS
	err = filepath.Walk(path.Join(current_path, "pkg"), pathForGoExt)
	dieIf(err)

	lib_dirs = createSet(tempDirs)

	gopath := filepath.Join(current_path, "_build")

	// setting GOPATH environmental variable
	if err := os.Setenv("GOPATH", gopath); err != nil {
		log.Fatal(err)
	}
	log.Printf("GOPATH is set to %s", gopath)

	log.Printf("running go install")

	// doing some slice magic to prepend install and pass the slice as args
	err = run(current_path, "go", append([]string{"install"}, k8_cmd...)...)
	dieIf(err)

	log.Printf("running go install with ldflags")
	err = run("", "go", append([]string{"install", "-ldflags", "\"\""}, k8_cmd...)...)
	dieIf(err)
}

func prep(current_path string) {
	log.Println("prep")

	buildPath := filepath.Join(current_path, "_build")
	// remove the existing path

	err := os.RemoveAll(filepath.Join(buildPath, "/src/github.com/mesosphere/kubernetes-mesos"))
	dieIf(err)

	// remove the softlink
	err = os.RemoveAll(filepath.Base(k8sm_go_package))
	dieIf(err)

	// getting the dirname (like in the MAKEFILE)
	mesospherePath := filepath.Dir(filepath.Join(buildPath, "src", k8sm_go_package))

	log.Printf("Creating directory %s", mesospherePath)
	// creating the directory path with default rights
	err = os.MkdirAll(mesospherePath, 0755)
	dieIf(err)

	log.Printf("Creating the softlink %s in %s", filepath.Base(current_path), mesospherePath)
	// creating the softlink in the mesospherePath
	err = run(mesospherePath, "ln", "-s", current_path, filepath.Base(current_path))
	dieIf(err)

}

func install(current_path string) {

	// removing target directory if exists
	err := os.RemoveAll(target)
	dieIf(err)

	// Creating the target directory
	err = os.Mkdir(target, 0755)
	dieIf(err)

	// walking the bin directory and copying files to target
	filepath.Walk(filepath.Join(current_path, "_build", "bin"), func(path string, _ os.FileInfo, _ error) error {
		f, err := os.Stat(path)
		dieIf(err)
		if !f.IsDir() {
			err = run("", "cp", "-vpf", path, target)
			dieIf(err)
		}
		return nil

	})
}

func usage() {
	fmt.Println(`
Prerequisites:
Have Go installed, version 1.4 minimum

Usage:


	go run makefile.go [command]


Available commands:

help
install
all
`)
	os.Exit(1)

}

func main() {

	kube_go_package = "github.com/GoogleCloudPlatform/kubernetes"
	k8sm_go_package = "github.com/mesosphere/kubernetes-mesos"

	k8_cmd = []string{path.Join(kube_go_package, "/cmd/kubectl"), path.Join(kube_go_package, "/cmd/kube-apiserver"), path.Join(kube_go_package, "/cmd/kube-proxy")}

	current_path, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	// parsing flags
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		usage()
		os.Exit(0)
	}
	switch args[0] {
	case "all":
		prep(current_path)
		all(current_path)
	case "install":
		prep(current_path)
		all(current_path)
		install(current_path)
	case "help":
		usage()
	default:
		usage()
	}
}
