package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
)

type NetLoggerWriter struct {
	socket   *net.UDPConn
	receiver *net.UDPAddr
}

var net_logger *log.Logger = nil
var net_log_server = flag.String("net_log_server", "127.0.0.1:6677", "Net log server.")

func (writer *NetLoggerWriter) initialize() error {
	address, err := net.ResolveUDPAddr("udp", *net_log_server)
	if err != nil {
		fmt.Printf("Failed to resolve net server %s, err %s", *net_log_server, err.Error())
		return err
	}
	writer.receiver = address

	sock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		fmt.Printf("Failed to create net log writer socket: %s", err.Error())
		return err
	}
	writer.socket = sock
	return nil
}

func (writer *NetLoggerWriter) Write(p []byte) (int, error) {
	if writer.socket == nil {
		return 0, errors.New("Log writer is not initialized")
	}
	fmt.Printf("%s", string(p))
	return writer.socket.WriteToUDP(p, writer.receiver)
}

func initNetLogger() error {
	writer := &NetLoggerWriter{}
	if err := writer.initialize(); err != nil {
		fmt.Println("Failed to initializse net log writer")
		return err
	}
	net_logger = log.New(writer, "", log.Ltime|log.Lshortfile)
	return nil
}
