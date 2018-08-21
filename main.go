package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"github.com/Masterminds/semver"
	log "github.com/sirupsen/logrus"
)

type DependencyMap struct {
	Registry        string
	SemverComponent string
	BasePath        string
	DockerImages    DockerImages
}

type DockerImages map[string]*DockerImage

type DockerImage struct {
	Image              string
	Version            string
	NewVersion         []string
	FromImage          string
	DockerFile         []string
	DockerFileFromLine int
}

func main() {
	//log.SetLevel(log.DebugLevel)
	args := os.Args[1:]
	rootFolder := filepath.Dir(args[0])

	di := generateDepenencyMap(rootFolder, "eu.gcr.io/karhoo-common")

	if true {
		semverComponent := args[1]
		primaryDockerImageFolder := filepath.Base(args[0])
		dm := DependencyMap{
			Registry:        "eu.gcr.io/karhoo-common",
			SemverComponent: semverComponent,
			BasePath:        rootFolder,
			DockerImages:    di,
		}
		log.Debugf("%v", dm)
		updateVersions([]string{primaryDockerImageFolder}, false, dm)
		buildDockerImages([]string{primaryDockerImageFolder}, false, dm)
	}
	di = generateDepenencyMap(rootFolder, "eu.gcr.io/karhoo-common")
	generateDependencyGraph(di, rootFolder)
}

func bumpVersion(version string, semverComponent string) (newVersion []string) {
	v, err := semver.NewVersion(version)
	if err != nil {
		log.Warnf("%s not semver so can't bump", version)
		return []string{version}
	}
	switch semverComponent {
	case "none":
		break
	case "major":
		*v = v.IncMajor()
	case "minor":
		*v = v.IncMinor()
	case "patch":
		*v = v.IncPatch()
	default:
		log.Fatalf("Don't understand semverComponent %s", semverComponent)
	}
	return []string{v.String(), fmt.Sprintf("%d.%d", v.Major(), v.Minor()), fmt.Sprintf("%d", v.Major())}
}

func updateVersionFile(folder string, dm DependencyMap) {
	newVersion := bumpVersion(dm.DockerImages[folder].Version, dm.SemverComponent)
	newContent := []byte(newVersion[0])
	file := fmt.Sprintf("%s/%s/VERSION", dm.BasePath, folder)
	if true {
		log.Info(fmt.Sprintf("Would write to '%s' to %s", newVersion[0], file))

	} else {
		err := ioutil.WriteFile(file, newContent, 0644)
		if err != nil {
			log.Fatalf("Couldn't write %s to file %s", newContent, file)
		}
	}
}

func updateDockerFile(folder string, dm DependencyMap) {
	dockerFile := dm.DockerImages[folder].DockerFile
	idx := dm.DockerImages[folder].DockerFileFromLine
	fromLine := dockerFile[idx]
	fromLineSplit := strings.Split(fromLine, ":")
	log.Debug(fromLineSplit)
	if len(fromLineSplit) > 2 {
		log.Fatalf("Can't parse FROM: %s", fromLine)
	}

	newVersion := bumpVersion(fromLineSplit[1], dm.SemverComponent)[0]
	newFromLine := fmt.Sprintf("%s:%s", fromLineSplit[0], newVersion)
	dockerFile[idx] = newFromLine

	newContent := []byte(strings.Join(dockerFile, "\n"))
	file := fmt.Sprintf("%s/%s/Dockerfile", dm.BasePath, folder)
	if true {
		log.Info(fmt.Sprintf("Would update %s FROM line to '%s'", file, newFromLine))
	} else {
		err := ioutil.WriteFile(file, newContent, 0644)
		if err != nil {
			log.Fatalf("Couldn't write %s to file %s", newContent, file)
		}
	}
}

func updateVersions(images []string, increment bool, dm DependencyMap) {
	for _, image := range images {
		updateVersionFile(image, dm)
		if increment {
			updateDockerFile(image, dm)
		}
		var dependentImages []string
		for key, dockerImage := range dm.DockerImages {
			//fmt.Printf("Comparing %s to %s\n", dockerImage.FromImage, dm.DockerImages[image].Image)
			if dockerImage.FromImage == dm.DockerImages[image].Image {
				dependentImages = append(dependentImages, key)
			}
		}
		if len(dependentImages) > 0 {
			updateVersions(dependentImages, true, dm)
		}
	}
}
func buildDockerImages(images []string, increment bool, dm DependencyMap) {
	var wg sync.WaitGroup
	for _, image := range images {
		wg.Add(1)
		go func(image string, dm DependencyMap, wg *sync.WaitGroup) {
			buildDockerImage(image, dm)
			var dependentImages []string
			for key, dockerImage := range dm.DockerImages {
				//fmt.Printf("Comparing %s to %s\n", dockerImage.From, dm.DockerImages[image].Image)
				if dockerImage.FromImage == dm.DockerImages[image].Image {
					dependentImages = append(dependentImages, key)
				}
			}
			if len(dependentImages) > 0 {
				buildDockerImages(dependentImages, true, dm)
			}
			wg.Done()
		}(image, dm, &wg)
	}
	wg.Wait()
}

func buildDockerImage(folder string, dm DependencyMap) {
	newVersion := bumpVersion(dm.DockerImages[folder].Version, dm.SemverComponent)
	newVersion = append(newVersion, "latest")
	image := fmt.Sprintf("%s/%s", "eu.gcr.io/karhoo-common", folder)
	tags := []string{}
	for _, tag := range newVersion {
		tags = append(tags, fmt.Sprintf("%s:%s", image, tag))
	}

	cmd := "docker"
	args := []string{"build"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	path := fmt.Sprintf("%s/%s", dm.BasePath, folder)
	args = append(args, path)
	if true {
		log.Info(fmt.Sprintf("Would build %s with tags %v", folder, tags))
	} else {
		log.Infof("Building %s", tags[0])
		output, err := exec.Command(cmd, args...).CombinedOutput()
		if err != nil {
			log.Fatalf("Docker build failed for %s\n%s", path, output)
		}
		log.Infof("Output of docker build %s\n%s", folder, string(output))
	}
}

func generateDependencyGraph(di DockerImages, basePath string) {
	dependencyGraphTemplate := `
digraph G {
  node [shape=rectangle];
  rankdir=LR;
  splines=polyline;
{{- range $_, $elem := . }}
{{ printf "  \"%s\" -> \"%s\";" $elem.FromImage $elem.Image }}
{{- end }}
}
	`
	t := template.Must(template.New("dependencyGraphTemplate").Parse(dependencyGraphTemplate))
	renderedScript := new(bytes.Buffer)

	err := t.Execute(renderedScript, di)
	if err != nil {
		log.Fatalf("Could not parse template: %+v", err)
	}
	stringData := renderedScript.String()
	file := fmt.Sprintf("%s/Dependency_Graph.dot", basePath)
	err = ioutil.WriteFile(file, []byte(stringData), 0644)
	if err != nil {
		log.Fatalf("Couldn't write %s to file %s", stringData, file)
	}
}

func generateDepenencyMap(path string, registry string) DockerImages {
	di := make(DockerImages)
	dirs, err := ioutil.ReadDir(path)
	if err != nil {
		log.Fatal(err)
	}
	for _, dir := range dirs {
		dirName := dir.Name()
		log.Debugf("Processing %s", dirName)

		dockerImage := DockerImage{}

		dockerFile, err := ioutil.ReadFile(fmt.Sprintf("%s/%s/Dockerfile", path, dirName))
		if err != nil {
			continue
		}
		dockerFileLines := strings.Split(string(dockerFile), "\n")
		for idx, line := range dockerFileLines {
			if strings.HasPrefix(line, "FROM") {
				dockerImage.FromImage = strings.Replace(line, "FROM ", "", 1)
				dockerImage.DockerFileFromLine = idx
				break
			}
		}
		dockerImage.DockerFile = dockerFileLines

		versionFile, _ := ioutil.ReadFile(fmt.Sprintf("%s/%s/VERSION", path, dirName))
		versionFileLines := strings.Split(string(versionFile), "\n")
		dockerImage.Version = strings.Replace(versionFileLines[0], "\n", "", 1)
		dockerImage.Image = fmt.Sprintf(fmt.Sprintf("%s/%s:%s", registry, dirName, dockerImage.Version))

		di[dirName] = &dockerImage
	}
	return di
}
