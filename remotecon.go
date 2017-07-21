package main

import (
	"io"
	"io/ioutil"
	"net"
	"os"

	log "github.com/HeroesAwaken/GoAwaken/Log"
	"golang.org/x/crypto/ssh"
)

// Get default location of a private key
func privateKeyPath() string {
	return os.Getenv("HOME") + "/.ssh/id_rsa"
}

// Get private key for ssh authentication
func parsePrivateKey(keyPath string) (ssh.Signer, error) {
	buff, _ := ioutil.ReadFile(keyPath)
	return ssh.ParsePrivateKey(buff)
}

// Get ssh client config for our connection
// SSH config will use 2 authentication strategies: by key and by password
func makeSshConfig(user string) (*ssh.ClientConfig, error) {
	key, err := parsePrivateKey(privateKeyPath())
	if err != nil {
		return nil, err
	}

	config := ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(key),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	return &config, nil
}

// Handle local client connections and tunnel data to the remote serverq
// Will use io.Copy - http://golang.org/pkg/io/#Copy
func handleClient(client net.Conn, sshClient *ssh.Client, remoteAddr string) {
	defer client.Close()
	chDone := make(chan bool)

	// Establish connection with remote server
	remote, err := sshClient.Dial("tcp", remoteAddr)
	if err != nil {
		log.Fatalln(err)
	}
	defer remote.Close()

	// Start remote -> local data transfer
	go func() {
		_, err := io.Copy(client, remote)
		if err != nil {
			log.Errorln("error while copy remote->local:", err)
		}
		chDone <- true
	}()

	// Start local -> remote data transfer
	go func() {
		_, err := io.Copy(remote, client)
		if err != nil {
			log.Errorln(err)
		}
		chDone <- true
	}()

	<-chDone
}

func startRemoveCon(sshAddr, localAddr, remoteAddr string) {

	// Build SSH client configuration
	cfg, err := makeSshConfig("forge")
	if err != nil {
		log.Fatalln(err)
	}

	// Establish connection with SSH server
	conn, err := ssh.Dial("tcp", sshAddr, cfg)
	if err != nil {
		log.Fatalln(err)
	}

	// Start local server to forward traffic to remote connection
	local, err := net.Listen("tcp", localAddr)
	if err != nil {
		log.Fatalln(err)
	}

	// Handle incoming connections
	go func() {
		for {
			client, err := local.Accept()
			if err != nil {
				log.Fatalln(err)
			}

			handleClient(client, conn, remoteAddr)
		}

		local.Close()
		conn.Close()
	}()
}
