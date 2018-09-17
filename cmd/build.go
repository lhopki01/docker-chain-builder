// Copyright © 2018 NAME HERE <EMAIL ADDRESS>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Masterminds/semver"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

var dryRun bool
var noPush bool

// buildCmd represents the build command
var buildCmd = &cobra.Command{
	Use:   "build <source folder> <major|minor|patch>",
	Short: "Build docker image and all docker images that depend on it",
	Long:  `Find all images that depend on a specific source image and build them in order`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 {
			return errors.New("Please specify source folder and semvar component")
		}
		if _, err := os.Stat(fmt.Sprintf("%s/Dockerfile", filepath.Clean(args[0]))); os.IsNotExist(err) {
			return errors.New(fmt.Sprintf("No Dockerfile in %s", args[0]))
		}
		switch args[1] {
		case
			"major",
			"minor",
			"patch",
			"pre":
			break
		default:
			return errors.New("Please specify one of ['major', 'minor', 'path', 'pre']")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		viper.Set("rootFolder", filepath.Dir(filepath.Clean(args[0])))
		viper.Set("imageFolder", filepath.Base(args[0]))
		viper.Set("semverComponent", args[1])
		loadConfFile()
		build()
	},
}

func init() {
	//log.SetLevel(log.DebugLevel)
	rootCmd.AddCommand(buildCmd)

	buildCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would happen")
	buildCmd.Flags().BoolVarP(&noPush, "no-push", "", false, "don't push images to registry")

}

func loadConfFile() {
	viper.SetConfigFile(fmt.Sprintf("%s/conf.yaml", viper.GetString("rootFolder")))
	err := viper.ReadInConfig()
	if err == nil {
		log.Infof("Using config file: %s", viper.ConfigFileUsed())
	} else {
		log.Warn("No conf file")
	}
}

func build() {
	//log.SetLevel(log.DebugLevel)
	rootFolder := viper.GetString("rootFolder")

	di := generateDepenencyMap(rootFolder, viper.GetString("registry"))

	primaryDockerImageFolder := viper.GetString("imageFolder")
	dm := DependencyMap{
		Registry:        viper.GetString("registry"),
		SemverComponent: viper.GetString("semverComponent"),
		BasePath:        rootFolder,
		DockerImages:    di,
	}
	log.Debugf("%v", dm)
	updateVersions([]string{primaryDockerImageFolder}, false, dm)
	buildDockerImages([]string{primaryDockerImageFolder}, false, dm)

	di = generateDepenencyMap(rootFolder, viper.GetString("registry"))
	generateDependencyGraph(di, rootFolder)
}

func bumpVersion(version string, semverComponent string) (newVersion []string) {
	log.Debugf("version string is: %s", version)
	v, err := semver.NewVersion(version)
	if err != nil {
		log.Warnf("%s not semver so can't bump", version)
		return []string{version}
	}
	preRelease := v.Prerelease()
	log.Debugf("preRelease is: %s", preRelease)
	newPreRelease := 0
	switch semverComponent {
	case "none":
		break
	case "major":
		*v = v.IncMajor()
	case "minor":
		*v = v.IncMinor()
	case "patch":
		*v = v.IncPatch()
	case "pre":
		newPreRelease, err = strconv.Atoi(preRelease)
		if err != nil {
			log.Warnf("Can't increment pre-release %s", preRelease)
		}
		newPreRelease = newPreRelease + 1
	default:
		log.Fatalf("Don't understand semverComponent %s", semverComponent)
	}
	if preRelease != "" {
		log.Debug("newPreRelease")
		*v, err = v.SetPrerelease(strconv.Itoa(newPreRelease))
		if err != nil {
			log.Fatalf("Failed to add pre-release %s with err %v", strconv.Itoa(newPreRelease), err)
		}
		return []string{v.String()}
	}
	return []string{v.String(), fmt.Sprintf("%d.%d", v.Major(), v.Minor()), fmt.Sprintf("%d", v.Major())}
}

func updateVersionFile(folder string, dm DependencyMap) {
	newVersion := bumpVersion(dm.DockerImages[folder].Version, dm.SemverComponent)
	newContent := []byte(newVersion[0])
	file := fmt.Sprintf("%s/%s/VERSION", dm.BasePath, folder)
	if dryRun {
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
	if dryRun {
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
	image := fmt.Sprintf("%s/%s", dm.Registry, folder)
	tags := []string{}
	for _, tag := range newVersion {
		tags = append(tags, fmt.Sprintf("%s:%s", image, tag))
	}

	cmd := "docker"
	args := []string{"build", "--pull"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	path := fmt.Sprintf("%s/%s", dm.BasePath, folder)
	args = append(args, path)
	if dryRun {
		log.Info(fmt.Sprintf("Would build %s with tags %v", folder, tags))
	} else {
		log.Infof("Building %s", tags[0])
		output, err := exec.Command(cmd, args...).CombinedOutput()
		if err != nil {
			log.Fatalf("Docker build failed for %s\n%s", path, output)
		}
		log.Infof("Output of docker build %s\n%s", folder, string(output))
	}
	if !noPush {
		for _, tag := range tags {
			if dryRun {
				log.Warnf("Would push %s", tag)
			} else {
				cmd := "docker"
				args := []string{"push", tag}
				output, err := exec.Command(cmd, args...).CombinedOutput()
				if err != nil {
					log.Fatalf("Docker push failed for %s\n%s", tag, output)
				}
				log.Infof("Output of docker push %s\n%s", tag, string(output))
			}
		}
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
