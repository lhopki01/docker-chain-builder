package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/docker/engine-api/client"
	"github.com/docker/engine-api/types"
	version "github.com/hashicorp/go-version"
	"github.com/mholt/archiver"
)

type DockerImage struct {
	Name       string
	From       string
	Dependents []DockerImage
}

func main() {
	args := os.Args[1:]
	primaryDockerImageFolder := args[0]
	//rootFolder := filepath.Dir(primaryDockerImageFolder)

	//dm := generateDepenencyMap(rootFolder)
	//generateDependencyGraph(dm)
	v1, _ := version.NewVersion("1.2.3")
	spew.Dump(v1.String())
	buildDockerImage(primaryDockerImageFolder)
}

func buildDockerImage(folder string) {
	ctx := context.Background()
	cli, err := client.NewClient("unix:///var/run/docker.sock", "v1.38", nil, nil)
	if err != nil {
		log.Fatal(err, " :unable to init client")
	}

	//buf := new(bytes.Buffer)
	//tw := tar.NewWriter(buf)
	//defer tw.Close()

	dockerFile := "Dockerfile"
	//dockerFileReader, err := os.Open(fmt.Sprintf("%s/Dockerfile", folder))
	//if err != nil {
	//	log.Fatal(err, " :unable to open Dockerfile")
	//}
	//readDockerFile, err := ioutil.ReadAll(dockerFileReader)
	//if err != nil {
	//	log.Fatal(err, " :unable to read dockerfile")
	//}

	//tarHeader := &tar.Header{
	//	Name: dockerFile,
	//	Size: int64(len(readDockerFile)),
	//}
	//err = tw.WriteHeader(tarHeader)
	//if err != nil {
	//	log.Fatal(err, " :unable to write tar header")
	//}
	//_, err = tw.Write(readDockerFile)
	//if err != nil {
	//	log.Fatal(err, " :unable to write tar body")
	//}
	_ = archiver.Tar.Make("temp.tar", []string{"folder"})
	dockerBuildContext, _ := os.Open("temp.tar")
	defer dockerBuildContext.Close()
	//dockerFileTarReader := bytes.NewReader(buf.Bytes())

	imageBuildResponse, err := cli.ImageBuild(
		ctx,
		//dockerFileTarReader,
		dockerBuildContext,
		types.ImageBuildOptions{
			//Context:    dockerFileTarReader,
			Context:    dockerBuildContext,
			Dockerfile: dockerFile,
			Remove:     true})
	if err != nil {
		log.Fatal(err, " :unable to build docker image")
	}
	defer imageBuildResponse.Body.Close()
	_, err = io.Copy(os.Stdout, imageBuildResponse.Body)
	if err != nil {
		log.Fatal(err, " :unable to read image build response")
	}
}

func generateDependencyGraph(dm map[string]string) {
	fmt.Print("digraph G {\n")
	for key, value := range dm {
		fmt.Printf("  \"%s\" -> \"%s\"\n", value, key)
	}
	fmt.Print("}\n")
}

func generateDepenencyMap(path string) map[string]string {
	dm := make(map[string]string)
	dirs, err := ioutil.ReadDir(path)
	if err != nil {
		log.Fatal(err)
	}
	for _, dir := range dirs {
		dirName := dir.Name()
		file, _ := os.Open(fmt.Sprintf("%s/%s/Dockerfile", path, dirName))
		reader := bufio.NewReader(file)
		line, _ := reader.ReadString('\n')
		from := strings.Replace(strings.Replace(line, "\n", "", 1), "FROM ", "", 1)
		dm[dirName] = from
		file.Close()
	}
	return dm
}
