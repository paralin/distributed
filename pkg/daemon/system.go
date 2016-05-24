package daemon

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	dc "github.com/fsouza/go-dockerclient"
	"github.com/synrobo/distributed/pkg/config"
	"github.com/synrobo/distributed/pkg/imagesync"
)

type System struct {
	HomeDir    string
	ConfigPath string

	Config        config.DistributedConfig
	ConfigLock    sync.Mutex
	ConfigWatcher *config.DistributedConfigWatcher
	DockerClient  *dc.Client

	ImageWorker *imagesync.ImageSyncWorker
}

func (s *System) initHomeDir() int {
	fmt.Printf("Using home directory %s...\n", s.HomeDir)
	// Check if home dir exists
	if _, err := os.Stat(s.HomeDir); os.IsNotExist(err) {
		fmt.Printf("Creating config directory %s...\n", s.HomeDir)
		if err := os.MkdirAll(s.HomeDir, 0777); err != nil {
			fmt.Printf("Unable to create config dir %s, error %s.\n", s.HomeDir, err)
			return 1
		}
	}

	s.ConfigPath = filepath.Join(s.HomeDir, "config.yaml")
	if !s.Config.CreateOrRead(s.ConfigPath) {
		fmt.Printf("Failed to create/read config at %s", s.ConfigPath)
		return 1
	}

	return 0
}

func (s *System) initWorkers() int {
	fmt.Printf("Initializing workers...\n")
	var err error

	s.DockerClient, err = s.Config.DockerConfig.BuildClient()
	if err != nil {
		fmt.Printf("Unable to create docker client, %v\n", err)
		return 1
	}

	iw := new(imagesync.ImageSyncWorker)
	iw.ConfigLock = &s.ConfigLock
	iw.DockerClient = s.DockerClient
	iw.Config = &s.Config
	iw.Init()
	s.ImageWorker = iw
	return 0
}

func (s *System) initWatchers() int {
	s.ConfigWatcher = new(config.DistributedConfigWatcher)
	s.ConfigWatcher.ConfigPath = &s.ConfigPath
	if res := s.ConfigWatcher.Init(); res != 0 {
		return res
	}
	return 0
}

// Wake the workers upon a config change
func (s *System) wakeWorkers() {
	fmt.Printf("Config changed, waking workers...\n")
	s.ImageWorker.WakeChannel <- true
}

func (s *System) closeWorkers() {
	s.ImageWorker.Quit()
}

func (s *System) closeWatchers() {
	s.ConfigWatcher.Close()
}

func (s *System) Main() int {
	if res := s.initHomeDir(); res != 0 {
		return res
	}

	if res := s.initWorkers(); res != 0 {
		return res
	}

	if res := s.initWatchers(); res != 0 {
		return res
	}

	fmt.Printf("Starting image worker...\n")
	go s.ImageWorker.Run()

	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	keepRunning := true
	for keepRunning {
		select {
		case <-c:
			keepRunning = false
			break
		case event := <-s.ConfigWatcher.ConfigWatcher.Events:
			fmt.Printf("event:%s\n", event)
			s.closeWatchers()
			time.Sleep(1 * time.Second)
			s.ConfigLock.Lock()
			didread := s.Config.ReadFrom(s.ConfigPath)
			s.ConfigLock.Unlock()
			if didread {
				s.wakeWorkers()
			}
			s.initWatchers()
			continue
		}
	}
	fmt.Println("Exiting...\n")
	s.closeWorkers()
	s.closeWatchers()
	return 0
}
