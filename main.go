package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Masterminds/semver"
	log "github.com/sirupsen/logrus"
)

type DockerImage struct {
	Image              string
	Version            string
	From               string
	DockerFile         []string
	DockerFileFromLine int
}

func main() {
	//log.SetLevel(log.DebugLevel)
	args := os.Args[1:]
	semverComponent := args[1]
	rootFolder := filepath.Dir(args[0])
	primaryDockerImageFolder := filepath.Base(args[0])

	dm := generateDepenencyMap(rootFolder)
	//spew.Dump(dm)
	log.Debugf("%v", dm)
	buildImages(rootFolder, []string{primaryDockerImageFolder}, semverComponent, false, dm)
	//generateDependencyGraph(dl)
}

func bumpVersion(version string, semverComponent string) (newVersion string) {
	v, err := semver.NewVersion(version)
	if err != nil {
		log.Fatalf("Can't read version of %s", version)
	}
	switch semverComponent {
	case "major":
		*v = v.IncMajor()
	case "minor":
		*v = v.IncMinor()
	case "patch":
		*v = v.IncPatch()
	default:
		log.Fatalf("Don't understand semverComponent %s", semverComponent)
	}
	return v.String()
}

func updateVersionFile(basePath string, folder string, semverComponent string, dm map[string]DockerImage) {
	newContent := []byte(bumpVersion(dm[folder].Version, semverComponent))
	file := fmt.Sprintf("%s/%s/VERSION", basePath, folder)
	err := ioutil.WriteFile(file, newContent, 0644)
	if err != nil {
		log.Fatalf("Couldn't write %s to file %s", newContent, file)
	}
}

func updateDockerFile(basePath string, folder string, semverComponent string, dm map[string]DockerImage) {
	dockerFile := dm[folder].DockerFile
	idx := dm[folder].DockerFileFromLine
	fromLine := dockerFile[idx]
	fromLineSplit := strings.Split(fromLine, ":")
	log.Debug(fromLineSplit)
	if len(fromLineSplit) > 2 {
		log.Fatalf("Can't parse FROM: %s", fromLine)
	}

	newVersion := bumpVersion(fromLineSplit[1], semverComponent)
	dockerFile[idx] = fmt.Sprintf("%s:%s", fromLineSplit[0], newVersion)

	newContent := []byte(strings.Join(dockerFile, "\n"))
	file := fmt.Sprintf("%s/%s/Dockerfile", basePath, folder)
	err := ioutil.WriteFile(file, newContent, 0644)
	if err != nil {
		log.Fatalf("Couldn't write %s to file %s", newContent, file)
	}
}

func buildImages(basePath string, images []string, semverComponent string, increment bool, dm map[string]DockerImage) {
	var wg sync.WaitGroup
	for _, image := range images {
		wg.Add(1)
		go func(basePath string, image string, semverComponent string, dm map[string]DockerImage, wg *sync.WaitGroup) {
			updateVersionFile(basePath, image, semverComponent, dm)
			if increment {
				updateDockerFile(basePath, image, semverComponent, dm)
			}
			buildDockerImage(basePath, image, semverComponent, dm)
			var dependentImages []string
			for key, dockerImage := range dm {
				//fmt.Printf("Comparing %s to %s\n", dockerImage.From, dm[image].Image)
				if dockerImage.From == dm[image].Image {
					dependentImages = append(dependentImages, key)
				}
			}
			if len(dependentImages) > 0 {
				buildImages(basePath, dependentImages, semverComponent, true, dm)
			}
			wg.Done()
		}(basePath, image, semverComponent, dm, &wg)
	}
	wg.Wait()
}

func buildDockerImage(basePath string, folder string, semverComponent string, dm map[string]DockerImage) {
	newVersion := bumpVersion(dm[folder].Version, semverComponent)
	tag := fmt.Sprintf("%s/%s:%s", "eu.gcr.io/karhoo-common", folder, newVersion)
	log.Infof("Building %s", tag)
	path := fmt.Sprintf("%s/%s", basePath, folder)
	output, err := exec.Command("docker", "build", "-t", tag, path).Output()
	if err != nil {
		log.Fatalf("Docker build failed for %s", path)
	}
	log.Infof("Output of docker build %s\n%s", folder, string(output))
}

func generateDependencyGraph(dm []DockerImage) {
	fmt.Print("digraph G {\n")
	fmt.Print("node [shape=rectangle];\n")
	fmt.Print("rankdir=LR;\n")
	fmt.Print("splines=ortho;\n")
	for _, elem := range dm {
		fmt.Printf("  \"%s\" -> \"%s\";\n", elem.From, elem.Image)
	}
	fmt.Print("}\n")
}

func generateDepenencyMap(path string) map[string]DockerImage {
	dm := make(map[string]DockerImage)
	dirs, err := ioutil.ReadDir(path)
	if err != nil {
		log.Fatal(err)
	}
	for _, dir := range dirs {
		dirName := dir.Name()

		dockerImage := DockerImage{}

		dockerFile, _ := ioutil.ReadFile(fmt.Sprintf("%s/%s/Dockerfile", path, dirName))
		dockerFileLines := strings.Split(string(dockerFile), "\n")
		for idx, line := range dockerFileLines {
			if strings.HasPrefix(line, "FROM") {
				dockerImage.From = strings.Replace(line, "FROM ", "", 1)
				dockerImage.DockerFileFromLine = idx
				break
			}
		}
		dockerImage.DockerFile = dockerFileLines

		versionFile, _ := ioutil.ReadFile(fmt.Sprintf("%s/%s/VERSION", path, dirName))
		versionFileLines := strings.Split(string(versionFile), "\n")
		dockerImage.Version = strings.Replace(versionFileLines[0], "\n", "", 1)

		dockerImage.Image = fmt.Sprintf(fmt.Sprintf("%s%s:%s", "eu.gcr.io/karhoo-common/", dirName, dockerImage.Version))

		dm[dirName] = dockerImage
	}
	return dm
}
