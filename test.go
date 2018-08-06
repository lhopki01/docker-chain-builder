package main

import (
	"fmt"
	"path/filepath"

	"github.com/davecgh/go-spew/spew"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func main() {
	pflag.BoolP("dry-run", "n", false, "Set dry-run mode")
	pflag.StringP("registry", "r", "", "Registry to push images to")

	pflag.Parse()
	viper.BindPFlags(pflag.CommandLine)
	viper.AutomaticEnv()

	if len(pflag.Args()) != 2 {
		log.Fatal("Please specify both semverComponent and path to the docker image folder")
	}

	semverComponent := pflag.Arg(0)
	if !isValidSemverComponent(semverComponent) {
		log.Fatalf("Invalid semverComponent: %s", semverComponent)
	}
	rootFolder := filepath.Dir(pflag.Arg(1))
	primaryDockerImageFolder := filepath.Base(pflag.Arg(1))

	viper.SetConfigName("config")
	viper.AddConfigPath(rootFolder)
	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("Fatal error config file: %s \n", err))
	}

	spew.Dump(viper.GetString("registry"))
	spew.Dump(viper.GetBool("dry-run"))
	spew.Dump(semverComponent)
	spew.Dump(primaryDockerImageFolder)
}

func isValidSemverComponent(component string) bool {
	switch component {
	case
		"major",
		"minor",
		"patch":
		return true
	}
	return false
}
