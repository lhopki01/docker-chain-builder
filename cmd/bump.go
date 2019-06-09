package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	BumpVersions = []string{
		VersionPre,
		VersionPatch,
		VersionMinor,
		VersionMajor,
	}
)

var bumpCmd = &cobra.Command{
	Use:   fmt.Sprintf("bump <source folder(s)> --bump=[%s]", strings.Join(BumpVersions, "|")),
	Short: "Bump version in a docker image and all docker images that depend on it",
	Long: `Find all images that depend on specified source images and bump the versions in them.
Can take more than one source image.
Useful for choosing the version bump before running docker-chain-builder build --bump=none in CI`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("please specify at least one source folder")
		}
		for _, arg := range args {
			if _, err := os.Stat(fmt.Sprintf("%s/Dockerfile", filepath.Clean(arg))); os.IsNotExist(err) {
				return fmt.Errorf("no Dockerfile in %s\n", arg)
			}
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		viper.Set("rootFolder", filepath.Dir(filepath.Clean(args[0])))
		if !stringInSlice(bumpComponent, BumpVersions) {
			log.Fatalf("please specify --bump=[%s]", strings.Join(BumpVersions, "|"))
		}
		loadConfFile()
		if verbose {
			log.SetLevel(log.DebugLevel)
		} else {
			log.SetLevel(log.InfoLevel)
		}
		dm := DependencyMap{}
		dm.initDepencyMap(args)
		dm.updateVersions(dm.RootImages, false)
	},
}

func init() {
	rootCmd.AddCommand(bumpCmd)

	helpBump := fmt.Sprintf("semver component to bump [%s] Required", strings.Join(BumpVersions, "|"))

	bumpCmd.Flags().StringVar(&bumpComponent, "bump", VersionNone, helpBump)
	bumpCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would happen")
	bumpCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "verbose mode")
}
