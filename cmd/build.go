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
	"fmt"
	"html/template"
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
	Log             *bytes.Buffer
	Scroll          bool
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
	YOrigin            int
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

var dryRun bool
var noPush bool
var nonInteractive bool
var verbose bool

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
		if verbose {
			log.SetLevel(log.DebugLevel)
		} else {
			log.SetLevel(log.InfoLevel)
		}
		dm := DependencyMap{}
		dm.initDepencyMap()
		if nonInteractive || dryRun {
			dm.build()
		} else {
			var buf bytes.Buffer
			dm.Log = &buf
			log.SetOutput(dm.Log)
			go dm.build()
			gui(&dm)
		}
	},
}

func init() {
	rootCmd.AddCommand(buildCmd)

	buildCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would happen")
	buildCmd.Flags().BoolVarP(&noPush, "no-push", "", false, "don't push images to registry")
	buildCmd.Flags().BoolVarP(&nonInteractive, "non-interactive", "", false, "don't use the gui to display the build")
	buildCmd.Flags().BoolVarP(&verbose, "verbose", "", false, "verbose mode")

}

func loadConfFile() {
	viper.SetConfigFile(fmt.Sprintf("%s/conf.yaml", viper.GetString("rootFolder")))
	err := viper.ReadInConfig()
	if err == nil {
		log.Infof("using config file: %s", viper.ConfigFileUsed())
	} else {
		log.Warn("no conf file")
	}
}

func (dm *DependencyMap) initDepencyMap() {
	rootFolder := viper.GetString("rootFolder")
	di := generateDockerImagesMap(rootFolder, viper.GetString("registry"))

	dm.Registry = viper.GetString("registry")
	dm.SemverComponent = viper.GetString("semverComponent")
	dm.BasePath = rootFolder
	dm.DockerImages = di
	dm.Scroll = true
}

func (dm *DependencyMap) build() {
	//log.SetLevel(log.ErrorLevel)
	log.Debugf("%v", dm)
	primaryDockerImageFolder := viper.GetString("imageFolder")
	dm.updateVersions([]string{primaryDockerImageFolder}, false)
	dm.buildDockerImages([]string{primaryDockerImageFolder}, false)

	//di = generateDockerImagesMap(rootFolder, viper.GetString("registry"))
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
			log.Warnf("can't increment pre-release %s", preRelease)
		}
		newPreRelease = newPreRelease + 1
	case VersionMinor:
		*v = v.IncMinor()
	case VersionPatch:
		*v = v.IncPatch()
	case VersionMajor:
		*v = v.IncMajor()
	default:
		log.Fatalf("don't understand semverComponent %s", semverComponent)
	}
	if preRelease != "" {
		log.Debug("newPreRelease")
		*v, err = v.SetPrerelease(strconv.Itoa(newPreRelease))
		if err != nil {
			log.Fatalf("failed to add pre-release %s with err %v", strconv.Itoa(newPreRelease), err)
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
		log.Info(fmt.Sprintf("would write to '%s' to %s", newVersion[0], file))

	} else {
		err := ioutil.WriteFile(file, newContent, 0644)
		if err != nil {
			log.Fatalf("couldn't write %s to file %s", newContent, file)
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
		log.Fatalf("can't parse FROM: %s", fromLine)
	}

	newVersion := bumpVersion(fromLineSplit[1], dm.SemverComponent)[0]
	newFromLine := fmt.Sprintf("%s:%s", fromLineSplit[0], newVersion)
	dockerFile[idx] = newFromLine

	newContent := []byte(strings.Join(dockerFile, "\n"))
	file := fmt.Sprintf("%s/%s/Dockerfile", dm.BasePath, folder)
	if dryRun {
		log.Info(fmt.Sprintf("would update %s FROM line to '%s'", file, newFromLine))
	} else {
		err := ioutil.WriteFile(file, newContent, 0644)
		if err != nil {
			log.Fatalf("couldn't write %s to file %s", newContent, file)
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
	for _, folder := range images {
		wg.Add(1)
		go func(folder string, dm DependencyMap, wg *sync.WaitGroup) {
			err := dm.buildDockerImage(folder)
			if err != nil {
				return
			}
			var dependentImages []string
			for key, dockerImage := range dm.DockerImages {
				log.Debugf("Comparing %s to %s\n", dm.DockerImages[folder].FromImage, dm.DockerImages[folder].Image)
				if dockerImage.FromImage == dm.DockerImages[folder].Image {
					dependentImages = append(dependentImages, key)
				}
			}
			if len(dependentImages) > 0 {
				dm.buildDockerImages(dependentImages, true)
			}
			wg.Done()
		}(folder, *dm, &wg)
	}
	wg.Wait()
}

func (dm *DependencyMap) buildDockerImage(folder string) error {
	newVersion := bumpVersion(dm.DockerImages[folder].Version, dm.SemverComponent)
	newVersion = append(newVersion, "latest")
	image := fmt.Sprintf("%s/%s", dm.Registry, folder)
	tags := []string{}
	for _, tag := range newVersion {
		tags = append(tags, fmt.Sprintf("%s:%s", image, tag))
	}

	command := "docker"
	//args := []string{"build", "--pull"}
	args := []string{"build"}
	if !noPush {
		args = append(args, "--pull")
	}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	path := fmt.Sprintf("%s/%s", dm.BasePath, folder)
	args = append(args, path)

	cmd := exec.Command(command, args...)
	cmd.Stdout = dm.DockerImages[folder].Logs
	cmd.Stderr = dm.DockerImages[folder].Logs

	if dryRun {
		log.Info(fmt.Sprintf("would build %s with tags %v", folder, newVersion))
	} else {
		log.Infof("building %s", folder)
		dm.DockerImages[folder].BuildStatus = "building"
		err := cmd.Run()
		if err != nil {
			if nonInteractive {
				log.Infof("output of docker build %s\n%s", folder, dm.DockerImages[folder].Logs.String())
			}
			dm.DockerImages[folder].BuildStatus = "failure"
			log.Errorf("build failed for %s with err:\n%v", path, err)
			return err
		} else {
			if nonInteractive {
				log.Infof("output of docker build %s\n%s", folder, dm.DockerImages[folder].Logs.String())
			}
			log.Infof("build succeeded for %s", path)
		}
	}
	if !noPush {
		dm.DockerImages[folder].BuildStatus = "pushing"
		for _, tag := range tags {
			if dryRun {
				log.Warnf("would push %s", tag)
			} else {
				command := "docker"
				args := []string{"push", tag}
				cmd := exec.Command(command, args...)
				cmd.Stdout = dm.DockerImages[folder].Logs
				cmd.Stderr = dm.DockerImages[folder].Logs
				err := cmd.Run()
				if err != nil {
					dm.DockerImages[folder].BuildStatus = "failure"
					if nonInteractive {
						log.Infof("output of docker push %s\n%s", tag, dm.DockerImages[folder].Logs.String())
					}
					log.Errorf("push failed for %s with err:\n%s", tag, err)
					return err
				}
				if nonInteractive {
					log.Infof("output of docker push %s\n%s", tag, dm.DockerImages[folder].Logs.String())
				}
			}
		}
	}
	dm.DockerImages[folder].BuildStatus = "success"
	return nil
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
		log.Fatalf("could not parse template: %+v", err)
	}
	stringData := renderedScript.String()
	file := fmt.Sprintf("%s/Dependency_Graph.dot", basePath)
	err = ioutil.WriteFile(file, []byte(stringData), 0644)
	if err != nil {
		log.Fatalf("couldn't write %s to file %s", stringData, file)
	}
}

func generateDockerImagesMap(path string, registry string) DockerImages {
	di := make(DockerImages)
	dirs, err := ioutil.ReadDir(path)
	if err != nil {
		log.Fatal(err)
	}
	for _, dir := range dirs {
		dirName := dir.Name()
		log.Debugf("processing %s", dirName)

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
		dockerImage.Logs = &buf
		dockerImage.YOrigin = 0

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

	//g.Cursor = true
	g.Mouse = false
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
	if err := g.SetKeybinding("dockerLogs", gocui.KeyArrowLeft, gocui.ModNone, leftArrow); err != nil {
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
				g.Update(dm.dockerLogView)
				g.Update(dm.logsView)
			}
		}
	}()
	if err := g.MainLoop(); err != nil && err != gocui.ErrQuit {
		log.Panicln(err)
	}
}

func (dm *DependencyMap) layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	if v, err := g.SetView("images", -1, -1, maxX/3, maxY/3*2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Highlight = true
		v.SelFgColor = gocui.AttrBold

		dm.imagesView(g)
		if _, err := g.SetCurrentView("images"); err != nil {
			return err
		}
	}
	if v, err := g.SetView("logs", -1, maxY/3*2, maxX/3, maxY-2); err != nil {
		v.Autoscroll = true
		v.Wrap = true
		dm.logsView(g)
	}
	if v, err := g.SetView("dockerLogs", maxX/3, -1, maxX, maxY-2); err != nil {
		v.Autoscroll = true
		v.Wrap = true
		dm.dockerLogView(g)
	}
	if v, err := g.SetView("controls", -1, maxY-2, maxX, maxY); err != nil {
		fmt.Fprintln(v, "\u001b[37;1m[Ctrl-C]\u001b[0m Quit  \u001b[37;1m[Up/Down]\u001b[0m Select building docker image  \u001b[33mBuilding\u001b[0m  \u001b[36mPushing\u001b[0m  \u001b[31mFailed\u001b[0m  \u001b[32mDone\u001b[0m")
	}
	return nil
}

func (dm *DependencyMap) logsView(g *gocui.Gui) error {
	v, _ := g.View("logs")
	v.Clear()
	fmt.Fprintln(v, dm.Log.String())
	return nil
}

func (dm *DependencyMap) dockerLogView(g *gocui.Gui) error {
	v, _ := g.View("images")
	_, cy := v.Cursor()
	line, _ := v.Line(cy)

	reg, err := regexp.Compile("[^a-zA-Z0-9-]+")
	if err != nil {
		log.Fatal(err)
	}
	image := reg.ReplaceAllString(strings.TrimSpace(line), "")

	v, _ = g.View("dockerLogs")
	dockerImage, ok := dm.DockerImages[image]
	if ok {
		v.Clear()
		//v.SetOrigin(0, dockerImage.YOrigin)
		//v.SetOrigin(0, 0)
		if dm.Scroll {
			v.Autoscroll = true
		} else {
			v.Autoscroll = false
		}
		logs := dockerImage.Logs.String()
		regex, _ := regexp.Compile("\r\n")
		logs = regex.ReplaceAllString(logs, "\n")
		regex, _ = regexp.Compile("\r")
		logs = regex.ReplaceAllString(logs, "\n")
		fmt.Fprintln(v, logs)
		//ox, oy := v.Origin()
		//dm.DockerImages[image].YOrigin = oy
		//fmt.Fprintln(v, fmt.Sprintf("x: %d, y: %d", ox, oy))
	}
	return nil
}

func (dm *DependencyMap) imagesView(g *gocui.Gui) error {
	v, _ := g.View("images")
	_, cy := v.Cursor()
	line, _ := v.Line(cy)

	reg, err := regexp.Compile("[^a-zA-Z0-9-]+")
	if err != nil {
		log.Fatal(err)
	}
	image := reg.ReplaceAllString(strings.TrimSpace(line), "")
	cursorDockerImage, ok := dm.DockerImages[image]
	if ok {
		switch buildStatus := cursorDockerImage.BuildStatus; buildStatus {
		case "building":
			v.SelFgColor = gocui.ColorYellow | gocui.AttrBold
		case "pushing":
			v.SelFgColor = gocui.ColorBlue | gocui.AttrBold
		case "failure":
			v.SelFgColor = gocui.ColorRed | gocui.AttrBold
		case "success":
			v.SelFgColor = gocui.ColorGreen | gocui.AttrBold
		default:
			v.SelFgColor = gocui.AttrBold
		}
	}

	//v, _ := g.View("images")
	v.Clear()
	primaryDockerImageFolder := viper.GetString("imageFolder")

	baseImage := fmt.Sprint(dm.DockerImages[primaryDockerImageFolder].Image)
	dockerImage, ok := dm.DockerImages[primaryDockerImageFolder]
	if ok {
		switch buildStatus := dockerImage.BuildStatus; buildStatus {
		case "building":
			fmt.Fprintln(v, fmt.Sprintf("\u001b[33m%s\u001b[0m", dm.DockerImages[primaryDockerImageFolder].Name))
		case "pushing":
			fmt.Fprintln(v, fmt.Sprintf("\u001b[36m%s\u001b[0m", dm.DockerImages[primaryDockerImageFolder].Name))
		case "failure":
			fmt.Fprintln(v, fmt.Sprintf("\u001b[31m%s\u001b[0m", dm.DockerImages[primaryDockerImageFolder].Name))
		case "success":
			fmt.Fprintln(v, fmt.Sprintf("\u001b[32m%s\u001b[0m", dm.DockerImages[primaryDockerImageFolder].Name))
		default:
			fmt.Fprintln(v, fmt.Sprintf("\u001b[0m%s", dm.DockerImages[primaryDockerImageFolder].Name))
		}
	}
	dm.printDependencies(v, baseImage, "  ")

	//lines := v.BufferLines()
	//for _, line := range lines {
	//	fmt.Fprintln(v, strconv.Quote(line))
	//}
	return nil
}

func (dm *DependencyMap) printDependencies(v *gocui.View, baseImage string, indentation string) {
	//keep a set order
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
				case "pushing":
					fmt.Fprintln(v, fmt.Sprintf("%s%c \u001b[36m%s\u001b[0m", indentation, 8627, dm.DockerImages[key].Name))
				case "failure":
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
		if cy < len(v.ViewBufferLines())-2 {
			if err := v.SetCursor(cx, cy+1); err != nil {
				ox, oy := v.Origin()
				if err := v.SetOrigin(ox, oy+1); err != nil {
					return err
				}
			}
		} else {
			if v.Name() == "dockerLogs" {
				dm.Scroll = true
			}
		}
	}
	if v.Name() == "imagesView" {
		v, _ := g.View("dockerLogs")
		v.Clear()
		v.SetOrigin(0, 0)
		//v.SetCursor(0, len(v.ViewBufferLines())-2)
	}
	g.Update(dm.imagesView)
	g.Update(dm.dockerLogView)
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
	if v.Name() == "dockerLogs" {
		dm.Scroll = false
	}
	if v.Name() == "imagesView" {
		v, _ := g.View("dockerLogs")
		v.Clear()
		v.SetOrigin(0, 0)
		//v.SetCursor(0, len(v.ViewBufferLines())-2)
	}
	g.Update(dm.imagesView)
	g.Update(dm.dockerLogView)
	return nil
}

func rightArrow(g *gocui.Gui, v *gocui.View) error {
	g.Cursor = true
	_, err := g.SetCurrentView("dockerLogs")
	return err
}

func leftArrow(g *gocui.Gui, v *gocui.View) error {
	g.Cursor = false
	_, err := g.SetCurrentView("images")
	return err
}
