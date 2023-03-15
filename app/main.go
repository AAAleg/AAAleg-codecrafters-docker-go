package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"syscall"
	"time"
)

type tokenAPIResponse struct {
	Token       string    `json:"token"`
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"`
	IssuedAt    time.Time `json:"issued_at"`
}

type FsLayer struct {
	BlobSum string `json:"blobSum"`
}

type Manifest struct {
	Name     string    `json:"name"`
	Tag      string    `json:"tag"`
	FsLayers []FsLayer `json:"fsLayers"`
}

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	image := os.Args[2]

	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	chrootDir := os.TempDir()

	if err := copyExecutableIntoDir(chrootDir, command); err != nil {
		fmt.Printf("copy executable err: %v", err)
		os.Exit(1)
	}

	if err := createDevNull(chrootDir); err != nil {
		fmt.Printf("error creating /dev/null: %v", err)
		os.Exit(1)
	}

	if err := syscall.Chroot(chrootDir); err != nil {
		fmt.Printf("chroot err: %v", err)
		os.Exit(1)
	}

	token, err := getBearerToken(image)
	if err != nil {
		fmt.Printf("Auth err: %v", err)
		os.Exit(1)
	}

	manifest, err := fetchManifest(token, image)
	if err != nil {
		fmt.Printf("error fetching manifest: %v", err)
		os.Exit(1)
	}

	err = extractImage(chrootDir, token, image, manifest)
	if err != nil {
		fmt.Printf("error extracting image: %v", err)
		os.Exit(1)
	}

	cmd := exec.Command(command, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if exitError, ok := err.(*exec.ExitError); ok {
		os.Exit(exitError.ExitCode())
	} else if err != nil {
		fmt.Printf("Err: %v", err)
		os.Exit(1)
	}

}

func copyExecutableIntoDir(chrootDir string, executablePath string) error {
	executablePathInChrootDir := path.Join(chrootDir, executablePath)

	if err := os.MkdirAll(path.Dir(executablePathInChrootDir), 0750); err != nil {
		return err
	}

	return CopyFile(executablePath, executablePathInChrootDir)
}

func CopyFile(sourceFilePath string, destinationFilePath string) error {
	sourceFileStat, err := os.Stat(sourceFilePath)
	if err != nil {
		return err
	}

	sourceFile, err := os.Open(sourceFilePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destinationFile, err := os.OpenFile(destinationFilePath, os.O_RDWR|os.O_CREATE, sourceFileStat.Mode())
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	_, err = io.Copy(destinationFile, sourceFile)
	return err
}

func createDevNull(chrootDir string) error {
	if err := os.MkdirAll(path.Join(chrootDir, "dev"), 0750); err != nil {
		return err
	}

	return os.WriteFile(path.Join(chrootDir, "dev", "null"), []byte{}, 0644)
}

func getBearerToken(repo string) (string, error) {
	var apiResponse tokenAPIResponse
	service := "registry.docker.io"
	url := fmt.Sprintf("http://auth.docker.io/token?service=%s&scope=repository:library/%s:pull", service, repo)
	response, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to call http://auth.docker.io/token: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read http response body: %w", err)
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return "", fmt.Errorf("failed to parse http response: %w", err)
	}

	return apiResponse.Token, nil
}

func fetchManifest(token, repo string) (*Manifest, error) {
	tag := "latest"
	url := fmt.Sprintf("https://registry-1.docker.io/v2/library/%s/manifests/%s", repo, tag)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read http response body: %w", err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer  %s", token))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to read http response body: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read http response body: %w", err)
	}

	var manifest Manifest
	return &manifest, json.Unmarshal(body, &manifest)
}

func extractImage(dest, token, repo string, manifest *Manifest) error {
	for index, blobSum := range manifest.FsLayers {
		if err := fetchLayer(dest, token, repo, blobSum, index); err != nil {
			return err
		}
	}
	return nil
}

func fetchLayer(dest, token, repo string, fsLayer FsLayer, index int) error {
	url := fmt.Sprintf("https://registry-1.docker.io/v2/library/%s/blobs/%s", repo, fsLayer.BlobSum)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to read http response body: %w", err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer  %s", token))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to read http response body: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == 307 {
		redirectUrl := res.Header.Get("location")
		req, err := http.NewRequest(http.MethodGet, redirectUrl, nil)
		if err != nil {
			return fmt.Errorf("failed to read http response body: %w", err)
		}
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))

		res, err = http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to read http response body: %w", err)
		}
		defer res.Body.Close()
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read http response body: %w", err)
	}

	tarball := fmt.Sprintf("%s.tar", fsLayer.BlobSum)
	if err := os.WriteFile(tarball, data, 0644); err != nil {
		return err
	}
	defer os.Remove(tarball)

	cmd := exec.Command("tar", "xpf", tarball, "-C", dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
