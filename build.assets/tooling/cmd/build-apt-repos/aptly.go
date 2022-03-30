package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aptly-dev/aptly/aptly"
	"github.com/aptly-dev/aptly/database"
	"github.com/aptly-dev/aptly/deb"
	"github.com/aptly-dev/aptly/files"
	"github.com/aptly-dev/aptly/pgp"
	log "github.com/sirupsen/logrus"
)

// This provides wrapper functions for the Aptly command. Aptly is written in Go but it doesn't appear
// to have a good binary API to use, only a CLI tool and REST API.

type Aptly struct {
	rootdir           string
	db                database.Storage
	collectionFactory *deb.CollectionFactory
}

func SetupAptly(dir string) (*Aptly, error) {
	db, err := database.NewOpenDB(filepath.Join(dir, "aptly.db"))
	if err != nil {
		return nil, err
	}

	return &Aptly{
		rootdir:           dir,
		db:                db,
		collectionFactory: deb.NewCollectionFactory(db),
	}, nil
}

func (apt *Aptly) Close() error {
	return apt.db.Close()
}

func (*Aptly) EnsureDefaultConfigExists() {
	// If the default config doesn't exist then it will be created the first time an Aptly command is
	// ran, which messes up the output.
	cmd := exec.Command("aptly", "repo", "list")
	err := cmd.Run()

	if err != nil {
		HandleFatalCommandError(err, "Failed to create default Aptly config", cmd)
	}
}

func (a *Aptly) CreateRepoIfNotExists(r *repo) (bool, error) {
	localRepos := a.collectionFactory.LocalRepoCollection()

	if _, err := localRepos.ByName(r.Name()); err == nil {
		log.Infof("Repo %s already exists", r.Name())
		return false, nil
	}

	log.Infof("Creating repo %s\n", r.Name())
	repo := deb.NewLocalRepo(r.Name(), "")
	repo.DefaultDistribution = r.osVersion
	repo.DefaultComponent = fmt.Sprintf("%s/%s", r.releaseChannel, r.majorVersion)

	err := localRepos.Add(repo)
	if err != nil {
		log.WithError(err).Errorf("Failed adding repo %s", r.Name())
		return false, err
	}

	return true, nil
}

// 	distributionArg := fmt.Sprintf("-distribution=%s", r.osVersion)
// 	componentArg := fmt.Sprintf("-component=%s/%s", r.releaseChannel, r.majorVersion)
// 	cmd := exec.Command("aptly", "repo", "create", distributionArg, componentArg, r.Name())
// 	err := cmd.Run()

// 	if err != nil {
// 		HandleFatalCommandError(err, "Failed to create repo \"%s\"", cmd, r.Name())
// 	}

// 	return true
// }

func (a *Aptly) DoesRepoExist(r *repo) bool {
	_, err := a.collectionFactory.LocalRepoCollection().ByName(r.Name())
	return err == nil
}

func (a *Aptly) GetExistingRepoNames() []string {

	// collectionFactory := context.NewCollectionFactory()
	// repos := make([]string, collectionFactory.LocalRepoCollection().Len())
	// i := 0
	// collectionFactory.LocalRepoCollection().ForEach(func(repo *deb.LocalRepo) error {
	// 	if raw {
	// 		repos[i] = repo.Name
	// 	} else {
	// 		e := collectionFactory.LocalRepoCollection().LoadComplete(repo)
	// 		if e != nil {
	// 			return e
	// 		}

	// 		repos[i] = fmt.Sprintf(" * %s (packages: %d)", repo.String(), repo.NumPackages())
	// 	}
	// 	i++
	// 	return nil
	// })

	// context.CloseDatabase()

	return []string{}
}

func (a *Aptly) ImportDeb(r *repo, debPath string) error {
	log.Infof("Importing deb files from %s into  %s", debPath, r.Name())

	localRepos := a.collectionFactory.LocalRepoCollection()

	log.Debugf("Looking up repo %s", r.Name())
	aptlyRepo, err := localRepos.ByName(r.Name())
	if err != nil {
		log.Error("No such repo")
		return err
	}

	log.Debug("Loading repo data")
	err = localRepos.LoadComplete(aptlyRepo)
	if err != nil {
		log.Error("Repo load failed")
		return err
	}

	log.Debug("Loading packages")
	pkgList, err := deb.NewPackageListFromRefList(aptlyRepo.RefList(), a.collectionFactory.PackageCollection(), nil)
	if err != nil {
		log.Error("Repo load failed")
		return err
	}

	log.Debug("Collecting package files")
	packageFiles, otherFiles, failedFiles := deb.CollectPackageFiles([]string{debPath}, &aptly.ConsoleResultReporter{Progress: nil})
	for _, p := range packageFiles {
		log.Debug("Found: %s", p)
	}

	for _, p := range otherFiles {
		log.Debug("Other: %s", p)
	}

	log.Debug("Importing package files")
	processedFiles, failedImportFiles, err := deb.ImportPackageFiles(
		pkgList,
		packageFiles,
		false, &pgp.GoVerifier{},
		files.NewPackagePool(a.rootdir, false),
		a.collectionFactory.PackageCollection(),
		&aptly.ConsoleResultReporter{Progress: nil},
		nil,
		a.collectionFactory.ChecksumCollection())
	if err != nil {
		log.Error("Failed importing files")
		return err
	}

	failedFiles = append(failedFiles, failedImportFiles...)
	processedFiles = append(processedFiles, otherFiles...)

	log.Debug("Updating reflist")
	aptlyRepo.UpdateRefList(deb.NewPackageRefListFromPackageList(pkgList))

	log.Debug("Updating repo package files")
	err = localRepos.Update(aptlyRepo)
	if err != nil {
		log.Error("Failed updating pkg files")
		return err
	}

	for _, f := range failedFiles {
		log.Warnf("Failed adding %s", f)
	}

	return nil
}

// Is there a more idiomatic way of writing this?
func (a *Aptly) PublishRepos(repos []*repo) {
	repoNames := make([]string, len(repos))
	for i, repo := range repos {
		repoNames[i] = repo.Name()
	}

	args := []string{"publish", "repo"}
	if len(repos) > 1 {
		componentsArgument := fmt.Sprintf("-component=%s", strings.Repeat(",", len(repos)-1))
		args = append(args, componentsArgument)
	}
	args = append(args, repoNames...)
	args = append(args, repos[0].os)

	cmd := exec.Command("aptly", args...)
	err := cmd.Run()

	if err != nil {
		HandleFatalCommandError(err, "Failed to publish repos", cmd)
	}
}

func (a *Aptly) GetRootDir() string {
	cmd := exec.Command("aptly", "config", "show")
	output, err := cmd.Output()

	if err != nil {
		HandleFatalCommandError(err, "Failed retrieve Aptly config info", cmd)
	}

	var outputJson map[string]interface{}
	err = json.Unmarshal(output, &outputJson)
	if err != nil {
		handleFatalError(err, "Failed to unmarshal `%s` output JSON into map", strings.Join(cmd.Args, " "))
	}

	if rootDirValue, ok := outputJson["rootDir"]; !ok {
		HandleFatalIssue("Failed to find `rootDir` key in `%s` output JSON", strings.Join(cmd.Args, " "))
	} else {
		if rootDirString, ok := rootDirValue.(string); !ok {
			HandleFatalIssue("The `rootDir` key in `%s` output JSON is not of type `string`", strings.Join(cmd.Args, " "))
		} else {
			return rootDirString
		}
	}

	HandleFatalIssue("This code path should never be hit")
	return ""
}

func (a *Aptly) CreateReposFromBucket(bucketPath string) ([]*repo, error) {
	// The file tree that we care about here will be of the following structure:
	// `/<bucketPath>/<os>/dists/<os version>/<release channel>/<major version>/...`

	result := []*repo{}

	log.Debugf("Searching bucket in %s", bucketPath)

	osDirs, err := GetSubdirectories(bucketPath)
	if err != nil {
		return nil, err
	}

	for _, os := range osDirs {
		osVersionRoot := filepath.Join(bucketPath, os, "dists")
		osVersions, err := GetSubdirectories(osVersionRoot)
		if err != nil {
			return nil, err
		}

		for _, osVersion := range osVersions {
			releaseChannelRoot := filepath.Join(osVersionRoot, osVersion)
			releaseChannels, err := GetSubdirectories(releaseChannelRoot)
			if err != nil {
				return nil, err
			}

			for _, releaseChannel := range releaseChannels {
				majorVersions, err := GetSubdirectories(releaseChannelRoot, releaseChannel)
				if err != nil {
					return nil, err
				}

				for _, majorVersion := range majorVersions {
					r := &repo{
						os:             os,
						osVersion:      osVersion,
						releaseChannel: releaseChannel,
						majorVersion:   majorVersion,
					}

					created, err := a.CreateRepoIfNotExists(r)
					if err != nil {
						return nil, err
					}

					if created {
						result = append(result, r)
					}
				}
			}
		}
	}

	return result, nil
}

func (a *Aptly) CreateReposFromArtifactRequirements(supportedOSInfo map[string][]string,
	releaseChannel string, majorVersion string) <-chan *repo {
	out := make(chan *repo)

	go func() {
		defer close(out)
		for os, osVersions := range supportedOSInfo {
			for _, osVersion := range osVersions {
				r := &repo{
					os:             os,
					osVersion:      osVersion,
					releaseChannel: releaseChannel,
					majorVersion:   majorVersion,
				}

				created, err := a.CreateRepoIfNotExists(r)
				if err != nil {
					handleFatalError(err, "kaboom")
				}

				if created {
					out <- r
				}
			}
		}
	}()

	return out
}
