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
	"bufio"
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver"
	"github.com/jroimartin/gocui"
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
	Name               string
	Image              string
	Version            string
	NewVersion         []string
	FromImage          string
	DockerFile         []string
	DockerFileFromLine int
	Logs               *bytes.Buffer
	BuildStatus        string
}

const (
	VersionNone  = "none"
	VersionPre   = "pre"
	VersionPatch = "patch"
	VersionMinor = "minor"
	VersionMajor = "major"
)

var (
	Versions = []string{
		VersionNone,
		VersionPre,
		VersionPatch,
		VersionMinor,
		VersionMajor,
	}
)

const (
	InfoColor    = "\033[1;34m%s\033[0m"
	NoticeColor  = "\033[1;36m%s\033[0m"
	WarningColor = "\033[1;33m%s\033[0m"
	ErrorColor   = "\033[1;31m%s\033[0m"
	DebugColor   = "\033[0;36m%s\033[0m"
)

var dryRun bool
var noPush bool

// buildCmd represents the build command
var buildCmd = &cobra.Command{
	Use:   fmt.Sprintf("build <source folder> <%s>", strings.Join(Versions, "|")),
	Short: "Build docker image and all docker images that depend on it",
	Long:  `Find all images that depend on a specific source image and build them in order`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 {
			return fmt.Errorf("please specify source folder and semvar component")
		}
		if _, err := os.Stat(fmt.Sprintf("%s/Dockerfile", filepath.Clean(args[0]))); os.IsNotExist(err) {
			return fmt.Errorf("no Dockerfile in %s", args[0])
		}
		if !isValidVersion(args[1]) {
			return fmt.Errorf("please specify one of %s", Versions)
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		viper.Set("rootFolder", filepath.Dir(filepath.Clean(args[0])))
		viper.Set("imageFolder", filepath.Base(args[0]))
		viper.Set("semverComponent", args[1])
		loadConfFile()
		dm := DependencyMap{}
		dm.initDepencyMap()
		go dm.build()
		gui(&dm)
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

func (dm *DependencyMap) initDepencyMap() {
	rootFolder := viper.GetString("rootFolder")
	di := generateDependencyMap(rootFolder, viper.GetString("registry"))

	dm.Registry = viper.GetString("registry")
	dm.SemverComponent = viper.GetString("semverComponent")
	dm.BasePath = rootFolder
	dm.DockerImages = di
}

func (dm *DependencyMap) build() {
	log.SetLevel(log.ErrorLevel)
	log.Debugf("%v", dm)
	primaryDockerImageFolder := viper.GetString("imageFolder")
	dm.updateVersions([]string{primaryDockerImageFolder}, false)
	dm.buildDockerImages([]string{primaryDockerImageFolder}, false)

	//di = generateDependencyMap(rootFolder, viper.GetString("registry"))
	//generateDependencyGraph(di, rootFolder)
}

func isValidVersion(version string) bool {
	for _, v := range Versions {
		if v == version {
			return true
		}
	}
	return false
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
	case VersionNone:
		break
	case VersionPre:
		newPreRelease, err = strconv.Atoi(preRelease)
		if err != nil {
			log.Warnf("Can't increment pre-release %s", preRelease)
		}
		newPreRelease = newPreRelease + 1
	case VersionMinor:
		*v = v.IncMinor()
	case VersionPatch:
		*v = v.IncPatch()
	case VersionMajor:
		*v = v.IncMajor()
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

func (dm *DependencyMap) updateVersionFile(folder string) {
	newVersion := bumpVersion(dm.DockerImages[folder].Version, dm.SemverComponent)
	newContent := []byte(newVersion[0] + "\n")
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

func (dm *DependencyMap) updateDockerFile(folder string) {
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

func (dm *DependencyMap) updateVersions(images []string, increment bool) {
	for _, image := range images {
		dm.updateVersionFile(image)
		if increment {
			dm.updateDockerFile(image)
		}
		var dependentImages []string
		for key, dockerImage := range dm.DockerImages {
			//fmt.Printf("Comparing %s to %s\n", dockerImage.FromImage, dm.DockerImages[image].Image)
			if dockerImage.FromImage == dm.DockerImages[image].Image {
				dependentImages = append(dependentImages, key)
			}
		}
		if len(dependentImages) > 0 {
			dm.updateVersions(dependentImages, true)
		}
	}
}
func (dm *DependencyMap) buildDockerImages(images []string, increment bool) {
	var wg sync.WaitGroup
	for _, image := range images {
		wg.Add(1)
		go func(image string, dm DependencyMap, wg *sync.WaitGroup) {
			dm.buildDockerImage(image)
			var dependentImages []string
			for key, dockerImage := range dm.DockerImages {
				//fmt.Printf("Comparing %s to %s\n", dockerImage.From, dm.DockerImages[image].Image)
				if dockerImage.FromImage == dm.DockerImages[image].Image {
					dependentImages = append(dependentImages, key)
				}
			}
			if len(dependentImages) > 0 {
				dm.buildDockerImages(dependentImages, true)
			}
			wg.Done()
		}(image, *dm, &wg)
	}
	wg.Wait()
}

func (dm *DependencyMap) buildDockerImage(folder string) {
	newVersion := bumpVersion(dm.DockerImages[folder].Version, dm.SemverComponent)
	newVersion = append(newVersion, "latest")
	image := fmt.Sprintf("%s/%s", dm.Registry, folder)
	tags := []string{}
	for _, tag := range newVersion {
		tags = append(tags, fmt.Sprintf("%s:%s", image, tag))
	}

	command := "docker"
	args := []string{"build", "--pull"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	path := fmt.Sprintf("%s/%s", dm.BasePath, folder)
	args = append(args, path)

	cmd := exec.Command(command, args...)
	//var buf bytes.Buffer
	//dm.DockerImages[folder].Logs = &buf
	cmd.Stdout = dm.DockerImages[folder].Logs
	cmd.Stderr = dm.DockerImages[folder].Logs

	if dryRun {
		log.Info(fmt.Sprintf("Would build %s with tags %v", folder, tags))
	} else {
		log.Infof("Building %s", tags[0])
		dm.DockerImages[folder].BuildStatus = "building"
		err := cmd.Run()
		if err != nil {
			dm.DockerImages[folder].BuildStatus = "failed"
			//log.Fatalf("Docker build failed for %s\n%s", path, output)
		} else {
			dm.DockerImages[folder].BuildStatus = "success"
		}
		//log.Infof("Output of docker build %s\n%s", folder, string(output))
		log.Infof("Docker build of %s resulted in %s", path, dm.DockerImages[folder].BuildStatus)
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

func (dm *DependencyMap) writeLogs(r io.Reader, folder string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		dm.DockerImages[folder].Logs.Write(scanner.Bytes())
	}
}

func outputLogs(scanner *bufio.Scanner) {
	for scanner.Scan() {
		m := scanner.Text()
		fmt.Println(m)
		//log.Printf(m)
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

func generateDependencyMap(path string, registry string) DockerImages {
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
		dockerImage.Name = dirName
		var buf bytes.Buffer
		//buf.WriteString(fmt.Sprintf("%s logs", dirName))
		dockerImage.Logs = &buf

		di[dirName] = &dockerImage
	}
	return di
}

func gui(dm *DependencyMap) {
	g, err := gocui.NewGui(gocui.OutputNormal)
	if err != nil {
		log.Panicln(err)
	}
	defer g.Close()

	g.Cursor = true
	g.Mouse = true
	g.SetManagerFunc(dm.layout)

	if err := g.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, quit); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyArrowDown, gocui.ModNone, dm.cursorDown); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyArrowUp, gocui.ModNone, dm.cursorUp); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("images", gocui.KeyArrowRight, gocui.ModNone, rightArrow); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("logs", gocui.KeyArrowLeft, gocui.ModNone, leftArrow); err != nil {
		log.Panicln(err)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				fmt.Println("Done!")
				return
			case <-ticker.C:
				g.Update(dm.imagesView)
				g.Update(dm.logView)
			}
		}
	}()
	if err := g.MainLoop(); err != nil && err != gocui.ErrQuit {
		log.Panicln(err)
	}
}

func (dm *DependencyMap) layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	//if v, err := g.SetView("hello", maxX/2-7, maxY/2, maxX/2+7, maxY/2+2); err != nil {
	if v, err := g.SetView("images", -1, -1, maxX/3, maxY-3); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Highlight = true
		v.SelFgColor = gocui.AttrBold
		//v.SelBgColor = gocui.ColorWhite
		//v.SelFgColor = gocui.ColorBlack

		dm.imagesView(g)
		if _, err := g.SetCurrentView("images"); err != nil {
			return err
		}
	}
	if v, err := g.SetView("logs", maxX/3, -1, maxX, maxY-3); err != nil {
		v.Autoscroll = true

		dm.logView(g)
	}
	if v, err := g.SetView("controls", -1, maxY-3, maxX, maxY); err != nil {
		fmt.Fprintln(v, "Ctrl-C: Quit")
	}
	return nil
}

func (dm *DependencyMap) logView(g *gocui.Gui) error {
	v, _ := g.View("images")
	_, cy := v.Cursor()
	line, _ := v.Line(cy)

	reg, err := regexp.Compile("[^a-zA-Z0-9-]+")
	if err != nil {
		log.Fatal(err)
	}
	image := reg.ReplaceAllString(strings.TrimSpace(line), "")

	v, _ = g.View("logs")
	logs, ok := dm.DockerImages[image]
	if ok {
		v.Clear()
		v.SetOrigin(0, 0)
		fmt.Fprintln(v, logs.Logs.String())
		ox, oy := v.Origin()
		fmt.Fprintln(v, fmt.Sprintf("x: %d y: %d", ox, oy))

	}
	return nil
}

func (dm *DependencyMap) imagesView(g *gocui.Gui) error {
	v, _ := g.View("images")
	v.Clear()
	//tag := viper.GetString("semverComponent")
	primaryDockerImageFolder := viper.GetString("imageFolder")

	baseImage := fmt.Sprint(dm.DockerImages[primaryDockerImageFolder].Image)
	dockerImage, ok := dm.DockerImages[primaryDockerImageFolder]
	if ok {
		switch buildStatus := dockerImage.BuildStatus; buildStatus {
		case "building":
			fmt.Fprintln(v, fmt.Sprintf("\u001b[33m%s\u001b[0m", dm.DockerImages[primaryDockerImageFolder].Name))
		case "failed":
			fmt.Fprintln(v, fmt.Sprintf("\u001b[31m%s\u001b[0m", dm.DockerImages[primaryDockerImageFolder].Name))
		case "success":
			fmt.Fprintln(v, fmt.Sprintf("\u001b[32m%s\u001b[0m", dm.DockerImages[primaryDockerImageFolder].Name))
		default:
			fmt.Fprintln(v, fmt.Sprintf("\u001b[0m%s", dm.DockerImages[primaryDockerImageFolder].Name))
		}
	}
	dm.printDependencies(v, baseImage, "  ")
	fmt.Fprintln(v, "----")

	return nil
}

func (dm *DependencyMap) printDependencies(v *gocui.View, baseImage string, indentation string) {
	keys := make([]string, 0, len(dm.DockerImages))
	for key := range dm.DockerImages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if dm.DockerImages[key].FromImage == baseImage {
			dockerImage, ok := dm.DockerImages[key]
			if ok {
				switch buildStatus := dockerImage.BuildStatus; buildStatus {
				case "building":
					fmt.Fprintln(v, fmt.Sprintf("%s%c \u001b[33m%s\u001b[0m", indentation, 8627, dm.DockerImages[key].Name))
				case "failed":
					fmt.Fprintln(v, fmt.Sprintf("%s%c \u001b[31m%s\u001b[0m", indentation, 8627, dm.DockerImages[key].Name))
				case "success":
					fmt.Fprintln(v, fmt.Sprintf("%s%c \u001b[32m%s\u001b[0m", indentation, 8627, dm.DockerImages[key].Name))
				default:
					fmt.Fprintln(v, fmt.Sprintf("%s%c \u001b[0m%s", indentation, 8627, dm.DockerImages[key].Name))
				}
			}
			dm.printDependencies(v, dm.DockerImages[key].Image, fmt.Sprintf("  %s", indentation))
		}
	}
}

func quit(g *gocui.Gui, v *gocui.View) error {
	return gocui.ErrQuit
}

func (dm *DependencyMap) cursorDown(g *gocui.Gui, v *gocui.View) error {
	if v != nil {
		cx, cy := v.Cursor()
		if err := v.SetCursor(cx, cy+1); err != nil {
			ox, oy := v.Origin()
			if err := v.SetOrigin(ox, oy+1); err != nil {
				return err
			}
		}
	}
	g.Update(dm.imagesView)
	g.Update(dm.logView)
	return nil
}

func (dm *DependencyMap) cursorUp(g *gocui.Gui, v *gocui.View) error {
	if v != nil {
		ox, oy := v.Origin()
		cx, cy := v.Cursor()
		if err := v.SetCursor(cx, cy-1); err != nil && oy > 0 {
			if err := v.SetOrigin(ox, oy-1); err != nil {
				return err
			}
		}
	}
	g.Update(dm.imagesView)
	g.Update(dm.logView)
	return nil
}

func rightArrow(g *gocui.Gui, v *gocui.View) error {
	//if v == nil || v.Name() == "images" {
	//	_, err := g.SetCurrentView("logs")
	//	return err
	//}
	//return nil
	_, err := g.SetCurrentView("logs")
	return err
}

func leftArrow(g *gocui.Gui, v *gocui.View) error {
	//if v == nil || v.Name() == "logs" {
	//	_, err := g.SetCurrentView("images")
	//	return err
	//}
	//return nil
	_, err := g.SetCurrentView("images")
	return err
}
