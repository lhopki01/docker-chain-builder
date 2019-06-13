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
	RootImages      []string
}

type DockerImages map[string]*DockerImage

type DockerImage struct {
	Name               string
	Image              string
	Version            string
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

var (
	bumpComponent  string
	sinceCommit    string
	dryRun         bool
	noCache        bool
	nonInteractive bool
	push           bool
	verbose        bool
)

// buildCmd represents the build command
var buildCmd = &cobra.Command{
	Use:   fmt.Sprintf("build <source folder(s)>"),
	Short: "Build docker image and all docker images that depend on it",
	Long: `Find all images that depend on specified source images and build them in order.
If multiple source folders are specified they are deduplicated and each dependency chain is only walked once.
All source folders must be in the same folder.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("please specify at least one source folder")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		viper.Set("rootFolder", filepath.Dir(filepath.Clean(args[0])))
		loadConfFile()
		if verbose {
			log.SetLevel(log.DebugLevel)
		} else {
			log.SetLevel(log.InfoLevel)
		}
		var buf bytes.Buffer
		if !nonInteractive && !dryRun {
			log.SetLevel(log.WarnLevel)
			log.SetOutput(&buf)
		}
		dm := DependencyMap{}
		dm.initDepencyMap(args)

		if nonInteractive || dryRun {
			dm.build()
		} else {
			dm.Log = &buf
			go dm.build()
			gui(&dm)
		}
	},
}

func init() {
	rootCmd.AddCommand(buildCmd)

	helpBump := fmt.Sprintf("semver component to bump [%s]", strings.Join(Versions, "|"))
	buildCmd.Flags().StringVar(&bumpComponent, "bump", VersionNone, helpBump)
	buildCmd.Flags().StringVar(&sinceCommit, "since-commit", "", "only images changes since specified commit")
	buildCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would happen")
	buildCmd.Flags().BoolVar(&noCache, "no-cache", false, "do not use cache when building the images")
	buildCmd.Flags().BoolVar(&push, "push", false, "push images to registry")
	buildCmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "don't use the gui to display the build")
	buildCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "verbose mode")
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

func imagesChangedSinceCommit(images []string) []string {
	cmd := exec.Command("git", "diff", "--ignore-all-space", "--name-only", sinceCommit, "--", "*")
	cmd.Dir = viper.GetString("rootFolder")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatal("Commit command failed")
	}
	lines := strings.Split(string(output), "\n")
	var changedRootFolders []string
	for _, line := range lines {
		for _, image := range images {
			match, err := filepath.Match(fmt.Sprintf("*/%s/*", image), filepath.Clean(line))
			if err != nil {
				log.Warnf("couldn't match image %s with %s", image, line)
			}
			if match {
				changedRootFolders = append(changedRootFolders, image)
			}
		}
	}
	return unique(changedRootFolders)
}

func unique(stringSlice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range stringSlice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func (dm *DependencyMap) initDepencyMap(args []string) {

	dm.Registry = viper.GetString("registry")

	if stringInSlice(bumpComponent, Versions) {
		dm.SemverComponent = bumpComponent
	} else {
		log.SetOutput(os.Stderr)
		log.Fatalf("%s invalid semver component; choose from %v", bumpComponent, Versions)
	}

	dm.BasePath = viper.GetString("rootFolder")

	dm.DockerImages = generateDockerImagesMap(dm.BasePath, dm.Registry)

	dm.RootImages = dm.getRootFolders(args)
}

func (dm *DependencyMap) getRootFolders(args []string) []string {
	var argImages []string
	for _, arg := range args {
		if _, err := os.Stat(fmt.Sprintf("%s/Dockerfile", filepath.Clean(arg))); err != nil {
			log.Warnf("no Dockerfile in %s\n", arg)
			continue
		} else {
			argImages = append(argImages, filepath.Base(arg))
		}
	}
	if sinceCommit != "" {
		argImages = imagesChangedSinceCommit(argImages)
	}

	var buildImages []string
	for _, argImage := range argImages {
		buildImages = append(buildImages, dm.getChildren(argImage)...)
	}
	log.Debugf("Going to build children %v", buildImages)

	var rootImages []string
	for _, argImage := range argImages {
		if !stringInSlice(argImage, buildImages) {
			rootImages = append(rootImages, argImage)
		}
	}
	log.Debugf("Root images are %v", rootImages)
	return rootImages
}

func stringInSlice(str string, slc []string) bool {
	for _, s := range slc {
		if s == str {
			return true
		}
	}
	return false
}

func (dm *DependencyMap) getChildren(folder string) []string {
	folderDockerImage, ok := dm.DockerImages[folder]
	if ok {
		var children []string
		for key, dockerImage := range dm.DockerImages {
			log.Debugf("Comparing %s to %s\n", dockerImage.FromImage, folderDockerImage.Image)
			if dockerImage.FromImage == folderDockerImage.Image {
				children = append(children, key)
			}
		}
		if len(children) > 0 {
			for _, child := range children {
				children = append(children, dm.getChildren(child)...)
			}
		}
		return unique(children)
	}
	return []string{}
}

func (dm *DependencyMap) build() {
	//log.SetLevel(log.ErrorLevel)
	log.Debugf("%v", dm)
	dm.updateVersions(dm.RootImages, false)
	dm.buildDockerImages(dm.RootImages)
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
			if dockerImage.FromImage == dm.DockerImages[image].Image {
				dependentImages = append(dependentImages, key)
			}
		}
		if len(dependentImages) > 0 {
			dm.updateVersions(dependentImages, true)
		}
	}
}
func (dm *DependencyMap) buildDockerImages(images []string) {
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
				dm.buildDockerImages(dependentImages)
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
	args := []string{"build"}
	if push {
		args = append(args, "--pull")
	}
	if noCache {
		args = append(args, "--no-cache")
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
	if push {
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
		dockerImage.Image = fmt.Sprintf("%s/%s:%s", registry, dirName, dockerImage.Version)
		dockerImage.Name = dirName
		var buf bytes.Buffer
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

		err = dm.imagesView(g)
		if err != nil {
			log.Error(err)
		}

		if _, err := g.SetCurrentView("images"); err != nil {
			return err
		}
	}
	if v, err := g.SetView("logs", -1, maxY/3*2, maxX/3, maxY-2); err != nil {
		v.Autoscroll = true
		v.Wrap = true
		err = dm.logsView(g)
		if err != nil {
			log.Error(err)
		}
	}
	if v, err := g.SetView("dockerLogs", maxX/3, -1, maxX, maxY-2); err != nil {
		v.Autoscroll = true
		v.Wrap = true
		err := dm.dockerLogView(g)
		if err != nil {
			log.Error(err)
		}
	}
	if v, err := g.SetView("controls", -1, maxY-2, maxX, maxY); err != nil {
		fmt.Fprintln(v, "\u001b[37;1m[Ctrl-C]\u001b[0m Quit  \u001b[37;1m[Up/Down]\u001b[0m Select image  \u001b[33mBuilding\u001b[0m  \u001b[36mPushing\u001b[0m  \u001b[31mFailed\u001b[0m  \u001b[32mDone\u001b[0m")
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
		err = v.SetOrigin(0, 0)
		if err != nil {
			log.Error(err)
		}

		logs := dockerImage.Logs.String()
		regex, _ := regexp.Compile("\r\n")
		logs = regex.ReplaceAllString(logs, "\n")
		regex, _ = regexp.Compile("\r")
		logs = regex.ReplaceAllString(logs, "\n")
		fmt.Fprintln(v, logs)
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

	v.Clear()
	for _, image := range dm.RootImages {
		dm.printImage(v, image, "")
		dm.printDependencies(v, dm.DockerImages[image].Image, "  ↳ ")
	}
	return nil
}

func (dm *DependencyMap) printDependencies(v *gocui.View, baseImage string, prefix string) {
	keys := make([]string, 0, len(dm.DockerImages))
	for key := range dm.DockerImages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if dm.DockerImages[key].FromImage == baseImage {
			dm.printImage(v, key, prefix)
			dm.printDependencies(v, dm.DockerImages[key].Image, fmt.Sprintf("  %s", prefix))
		}
	}
}

func (dm *DependencyMap) printImage(v *gocui.View, image string, prefix string) {
	dockerImage, ok := dm.DockerImages[image]
	if ok {
		switch buildStatus := dockerImage.BuildStatus; buildStatus {
		case "building":
			fmt.Fprintln(v, fmt.Sprintf("%s\u001b[33m%s\u001b[0m", prefix, dm.DockerImages[image].Name))
		case "pushing":
			fmt.Fprintln(v, fmt.Sprintf("%s\u001b[36m%s\u001b[0m", prefix, dm.DockerImages[image].Name))
		case "failure":
			fmt.Fprintln(v, fmt.Sprintf("%s\u001b[31m%s\u001b[0m", prefix, dm.DockerImages[image].Name))
		case "success":
			fmt.Fprintln(v, fmt.Sprintf("%s\u001b[32m%s\u001b[0m", prefix, dm.DockerImages[image].Name))
		default:
			fmt.Fprintln(v, fmt.Sprintf("%s\u001b[0m%s", prefix, dm.DockerImages[image].Name))
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
