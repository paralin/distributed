package imagesync

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/docker/distribution"
	"github.com/docker/distribution/context"
	"github.com/docker/distribution/reference"
	"github.com/docker/engine-api/types"
	ddistro "github.com/fuserobotics/distributed/pkg/distribution"

	dc "github.com/fsouza/go-dockerclient"
	"github.com/fuserobotics/distributed/pkg/config"
	"github.com/fuserobotics/distributed/pkg/registry"
)

type ImageSyncWorker struct {
	Config       *config.DistributedConfig
	ConfigLock   *sync.Mutex
	DockerClient *dc.Client

	Running     bool
	WakeChannel chan bool
	QuitChannel chan bool

	RegistryContext context.Context
}

func (iw *ImageSyncWorker) Init() {
	iw.Running = true
	iw.WakeChannel = make(chan bool, 1)
	iw.QuitChannel = make(chan bool, 1)
	iw.RegistryContext = context.Background()
}

func (iw *ImageSyncWorker) sleepShouldQuit(t time.Duration) bool {
	time.Sleep(t)
	select {
	case <-iw.QuitChannel:
		return true
	case <-iw.WakeChannel:
		return false
	default:
		return false
	}
}

type imageToFetch struct {
	NeededTags  []string
	AvailableAt map[string][]availableDownloadRepository
	Target      config.TargetImage
	Reference   reference.Named
}

type availableDownloadRepository struct {
	Repo    *distribution.Repository
	RepoRef *config.RemoteRepository
}

func buildImageReference(image string) (error, string, *reference.Named) {
	imagePts := strings.Split(image, "/")
	if len(imagePts) == 1 {
		image = strings.Join([]string{"library", image}, "/")
	}
	ref, err := reference.ParseNamed(image)
	if err != nil {
		fmt.Printf("Error parsing reference %s, %v.\n", image, err)
		return err, "", nil
	}
	return nil, image, &ref
}

func connectRemoteRepository(context context.Context, rege *config.RemoteRepository, ref reference.Named) (error, *distribution.Repository) {
	urlParsed, err := url.Parse(rege.Url)
	if err != nil {
		fmt.Printf("Unable to parse url %s, %v\n", rege.Url, err)
		return err, nil
	}
	var insecureRegs []string
	if rege.Insecure {
		insecureRegs = []string{urlParsed.Host}
	}
	service := registry.NewService(registry.ServiceOptions{InsecureRegistries: insecureRegs})
	info, err := registry.ParseRepositoryInfo(ref)
	if err != nil {
		fmt.Printf("Error parsing repository info %s, %v.\n", ref.Name(), err)
		return err, nil
	}
	endpoints, err := service.LookupPullEndpoints(urlParsed.Host)
	if err != nil {
		fmt.Printf("Error parsing endpoints %s, %v.\n", rege.Url, err)
		return err, nil
	}
	metaHeaders := rege.MetaHeaders
	authConfig := &types.AuthConfig{Username: rege.Username, Password: rege.Password}
	successfullyConnected := false
	// var endpoint registry.APIEndpoint
	var reg distribution.Repository
	for _, endp := range endpoints {
		reg, _, err = ddistro.NewV2Repository(context, info, endp, metaHeaders, authConfig, "pull")
		if err != nil {
			// fmt.Printf("Error connecting to '%s', %v\n", rege.Url, err)
			continue
		}
		successfullyConnected = true
		break
	}
	if !successfullyConnected {
		return err, nil
	}
	return nil, &reg
}

func (iw *ImageSyncWorker) Run() {
	doRecheck := true
	for iw.Running {
		fmt.Printf("ImageSyncWorker sleeping...\n")
		for !doRecheck {
			select {
			case <-iw.QuitChannel:
				fmt.Printf("ImageSyncWorker exiting...\n")
				return
			case <-iw.WakeChannel:
				fmt.Printf("ImageSyncWorker woken, re-checking...\n")
				doRecheck = true
				break
			}
		}
		doRecheck = false
		fmt.Printf("ImageSyncWorker checking repositories...\n")

		iw.ConfigLock.Lock()
		if len(iw.Config.RemoteRepos) == 0 {
			fmt.Printf("No repositories given in config.\n")
			iw.ConfigLock.Unlock()
			continue
		}
		iw.ConfigLock.Unlock()

		// Load the current image list from the Docker client
		// ... just in case something's there and not in the repo
		/*
			liOpts := dc.ListImagesOptions{}
			dcImages, err := iw.DockerClient.ListImages(liOpts)
			if err != nil {
				fmt.Printf("Error fetching images list %v\n", err)
				if iw.sleepShouldQuit(time.Duration(2 * time.Second)) {
					return
				}
				continue
			}
			dcImageMap := utils.BuildImageMap(&dcImages)
		*/

		// For each target image grab the local tag list.
		var imagesToFetch []*imageToFetch
		iw.ConfigLock.Lock()
		for _, img := range iw.Config.Images {
			err, image, ref := buildImageReference(img.Image)
			if err != nil {
				fmt.Printf("Unable to parse %s reference, %v\n", img.Image, err)
				continue
			}
			img.Image = image

			err, reg := connectRemoteRepository(iw.RegistryContext, &iw.Config.Repo, *ref)
			if err != nil {
				fmt.Printf("Unable to connect successfully to local repo %s, %v.\n", iw.Config.Repo.Url, err)
				continue
			}

			// query tags
			tags, err := (*reg).Tags(iw.RegistryContext).All(iw.RegistryContext)
			if err != nil {
				if strings.Contains(err.Error(), "repository name not known") {
					fmt.Printf("Local repo does not have any versions of %s.\n", img.Image)
				} else {
					fmt.Printf("Error querying local repo for tags of %s, %v\n", img.Image, err)
					continue
				}
			}

			// make a map of target tags
			targetTagMap := make(map[string]bool)
			for _, tag := range img.Versions {
				targetTagMap[tag] = true
			}

			fmt.Printf("Local repo has %d tags for %s\n", len(tags), img.Image)
			for _, tag := range tags {
				delete(targetTagMap, tag)
			}

			tagCnt := len(targetTagMap)
			if tagCnt == 0 {
				continue
			}

			tagArr := make([]string, tagCnt)
			i := 0
			for tag, _ := range targetTagMap {
				tagArr[i] = tag
				i++
			}

			toFetch := new(imageToFetch)
			toFetch.NeededTags = tagArr
			toFetch.Target = img
			toFetch.AvailableAt = make(map[string][]availableDownloadRepository)
			toFetch.Reference = *ref
			imagesToFetch = append(imagesToFetch, toFetch)
		}

		if len(imagesToFetch) == 0 {
			iw.ConfigLock.Unlock()
			continue
		}

		fmt.Printf("Preparing to fetch %d repos...\n", len(imagesToFetch))

		// Build registry client
		// Rebuild the registry list
		for _, rege := range iw.Config.RemoteRepos {
			for _, tf := range imagesToFetch {
				err, reg := connectRemoteRepository(iw.RegistryContext, &rege, tf.Reference)
				if err != nil {
					fmt.Printf("Unable to connect successfully to %s, %v.\n", rege.Url, err)
					continue
				}
				// tags is the tag service
				tags, err := (*reg).Tags(iw.RegistryContext).All(iw.RegistryContext)
				if err != nil {
					fmt.Printf("Error checking '%s' for %s, %v\n", rege.Url, tf.Reference.Name(), err)
					continue
				}
				fmt.Printf("From %s, %s is available with %d tags.\n", rege.Url, tf.Reference.Name(), len(tags))
				for _, tag := range tags {
					tf.AvailableAt[tag] = append(tf.AvailableAt[tag], availableDownloadRepository{
						Repo:    reg,
						RepoRef: &rege,
					})
				}
			}
			iw.ConfigLock.Unlock()

			for _, tf := range imagesToFetch {
				matchedOne := false
				for _, tag := range tf.NeededTags {
					for _, reg := range tf.AvailableAt[tag] {
						fmt.Printf("%s:%s available from %s, pulling...\n", tf.Target.Image, tag, reg.RepoRef.Url)
						popts := dc.PullImageOptions{
							Repository: tf.Target.Image,
							Tag:        tag,
							Registry:   reg.RepoRef.PullPrefix,
						}
						authopts := dc.AuthConfiguration{
							Username: reg.RepoRef.Username,
							Password: reg.RepoRef.Password,
						}
						err := iw.DockerClient.PullImage(popts, authopts)
						if err != nil {
							fmt.Printf("Failed to pull %s:%s from %s, %v\n", tf.Target.Image, tag, reg.RepoRef.Url)
							continue
						}
						matchedOne = true
						var imageTaggedName string
						if iw.Config.Repo.PullPrefix == "" {
							imageTaggedName = tf.Target.Image
							fmt.Printf("%s:%s pushing to docker hub (empty PullPrefix)...\n", imageTaggedName, tag)
						} else {
							imageTaggedName := strings.Join([]string{iw.Config.Repo.PullPrefix, tf.Target.Image}, "/")
							fmt.Printf("%s:%s tagging as %s:%s...\n", tf.Target.Image, tag, imageTaggedName, tag)
							tagopts := dc.TagImageOptions{
								Repo:  imageTaggedName,
								Tag:   tag,
								Force: true,
							}
							err = iw.DockerClient.TagImage(tf.Target.Image, tagopts)
							if err != nil {
								fmt.Printf("Failed to make tag on %s:%s: %v\n", tf.Target.Image, tag, err)
								break
							}
							fmt.Printf("%s:%s pushing to %s...\n", imageTaggedName, tag, iw.Config.Repo.PullPrefix)
						}
						iw.ConfigLock.Lock()
						authopts = dc.AuthConfiguration{
							Username: iw.Config.Repo.Username,
							Password: iw.Config.Repo.Password,
						}
						puopts := dc.PushImageOptions{
							Name:     imageTaggedName,
							Tag:      tag,
							Registry: iw.Config.Repo.PullPrefix,
						}
						iw.ConfigLock.Unlock()
						err = iw.DockerClient.PushImage(puopts, authopts)
						if err != nil {
							fmt.Printf("Failed to push %s:%s to %s, %v\n", tf.Target.Image, tag, puopts.Registry, err)
						}
						break
					}
					if matchedOne {
						break
					}
				}
			}

			// Flush the wake channel
			hasEvents := true
			for hasEvents {
				select {
				case _ = <-iw.WakeChannel:
					continue
				default:
					hasEvents = false
					break
				}
			}
		}
	}
	fmt.Printf("ImageSyncWorker exiting...\n")
}

func (iw *ImageSyncWorker) Quit() {
	if !iw.Running {
		return
	}
	iw.Running = false
	iw.QuitChannel <- true
}
