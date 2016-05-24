package config

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/go-yaml/yaml"
)

type DistributedConfig struct {
	DockerConfig DockerClientConfig "dockerConfig"
	Repo         RemoteRepository   "repo"
	RemoteRepos  []RemoteRepository "remoteRepos"
	Images       []TargetImage      "images"
}

func configFileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

func (c *DistributedConfig) writeConfig(path string) bool {
	fmt.Printf("Writing config to %s\n", path)

	d, err := yaml.Marshal(&c)
	if err != nil {
		fmt.Errorf("Error marshalling config: %v\n", err)
		return false
	}

	err = ioutil.WriteFile(path, d, 0644)
	return true
}

func (c *DistributedConfig) FillWithDefaults() {
	c.DockerConfig.FillWithDefaults()
}

func (c *DistributedConfig) ReadFrom(confPath string) bool {
	dat, err := ioutil.ReadFile(confPath)
	if err != nil {
		fmt.Printf("Unable to read config at %s, %v\n", confPath, err)
		return false
	}

	err = yaml.Unmarshal(dat, &c)
	if err != nil {
		fmt.Printf("Unable to parse config at %s, %v\n", confPath, err)
		return false
	}

	fmt.Printf("Read config from %s\n", confPath)
	c.FillWithDefaults()
	return true
}

func (c *DistributedConfig) CreateOrRead(confPath string) bool {
	if !configFileExists(confPath) {
		fmt.Printf("Writing default config to %s\n", confPath)
		c.FillWithDefaults()
		if !c.writeConfig(confPath) {
			fmt.Errorf("Unable to write default config!\n")
			return false
		}
		return true
	}

	return c.ReadFrom(confPath)
}
