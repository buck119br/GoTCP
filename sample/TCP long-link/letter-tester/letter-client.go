package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
)

type LetterClient struct {
	uid         uint32
	name        string
	sig         string
	server_host string
	server_port int
	hbtime      int
	msgid       string
	conn        *net.TCPConn
	reader      *bufio.Reader
	sync_chan   <-chan int
}

const (
	GET_SVR_URL = "http://test.kuaiyuzhibo.cn/kuaiyu/service/server.php?cmd=getmsgsvr&uid=%d&sig=xx&sysflag=kuaiyu827"
)

var receiver_uid = flag.Uint("receiver_uid", 98, "Send letter to whom")
var letter_content = flag.String("letter_content", "This is a test message", "Letter content")

func newLetterClient(uid uint32, nickname string, sync chan int) *LetterClient {
	client := &LetterClient{
		uid:       uid,
		name:      nickname,
		sync_chan: sync,
	}
	return client
}

func (client *LetterClient) getServerInfo() bool {
	url := fmt.Sprintf(GET_SVR_URL, client.uid)

	res, err := http.Get(url)
	if err != nil {
		log.Printf("Failed to get message server sig info: %s\n", err.Error())
		return false
	}
	defer res.Body.Close()

	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Printf("Failed to read get sig response: %s\n", err.Error())
		return false
	}

	response := struct {
		Error  int    `json:"error"`
		ErrMsg string `json:"errmsg"`
		Result struct {
			ServerHost string `json:"server_host"`
			ServerPort int    `json:"server_port"`
			Sig        string `json:"sig"`
		} `json:"result"`
	}{}
	err = json.Unmarshal(data, &response)
	if err != nil {
		log.Printf("Failed to unmarshal get sig result: %s(%s)\n", string(data), err.Error())
		return false
	}
	if response.Error != 0 {
		log.Printf("Invalid response code %d\n", response.Error)
		return false
	}
	client.server_host = response.Result.ServerHost
	client.server_port = response.Result.ServerPort
	client.sig = response.Result.Sig

	log.Printf("Uid %d Letter server %s:%d", client.uid, client.server_host, client.server_port)

	return true
}

func (client *LetterClient) composeLoginMessage() []byte {
	login_message := fmt.Sprintf("cmd=ap_login&uid=%d&nickname=%s&sig=%s&smtime=%d&timestamp=%d",
		client.uid, url.QueryEscape(client.name), url.QueryEscape(client.sig), time.Now().Unix(), time.Now().Unix())
	sign := create_signature([]byte(login_message))
	login_message += fmt.Sprintf("&reqsign=%d\n", sign)
	return []byte(login_message)
}

func (client *LetterClient) writeMessage(message []byte) bool {
	length, err := client.conn.Write(message)
	if err != nil {
		log.Printf("Failed to send message: %s (%s)\n", string(message), err.Error())
		return false
	}
	if length != len(message) {
		log.Printf("Invalid write result length %d != %d\n", length, len(message))
		return false
	}
	return true
}

func (client *LetterClient) readMessage() ([]byte, error) {
	data, err := client.reader.ReadString('\n')
	if err != nil {
		log.Printf("Failed to read message from server: %s\n", err.Error())
		return nil, err
	}
	return []byte(data), nil
}

func (client *LetterClient) doLogin() bool {
	letter_server := fmt.Sprintf("%s:%d", client.server_host, client.server_port)

	laddr := &net.TCPAddr{}
	raddr, _ := net.ResolveTCPAddr("tcp", letter_server)

	conn, err := net.DialTCP("tcp", laddr, raddr)
	if err != nil {
		log.Printf("Failed to dial on letter server %s\n", letter_server)
		return false
	}
	client.conn = conn
	client.reader = bufio.NewReader(client.conn)

	login_message := client.composeLoginMessage()
	log.Printf("Client %d login message %s\n", client.uid, login_message)

	if ok := client.writeMessage(login_message); !ok {
		log.Printf("Failed to write login message!")
		return false
	}
	data, err := client.readMessage()
	if err != nil {
		log.Printf("Failed to get login response: %s\n", err.Error())
		return false
	}
	log.Printf("Client %d login message %s", client.uid, string(data))

	login_response := struct {
		Type   string `json:"type"`
		Flag   string `json:"flag"`
		HBTime int    `json:"hbtime"`
		Error  int    `json:"error"`
		ErrMsg string `json:"errmsg"`
	}{}
	err = json.Unmarshal(data, &login_response)
	if err != nil {
		log.Printf("Failed to unmarshal login response: %s(%s)\n", string(data), err.Error())
		return false
	}
	if login_response.Error != 0 {
		log.Printf("Failed to login letter server: %s\n", login_response.ErrMsg)
		return false
	}
	if login_response.Type != "ap_login" {
		log.Printf("Invalid login response: %s\n", string(data))
		return false
	}
	client.hbtime = login_response.HBTime
	return true
}

func (client *LetterClient) sendHeartbeat() {
	for {
		time.Sleep(3 * time.Second)

		message := fmt.Sprintf("cmd=ap_heartbeat&uid=%d&timestamp=%d", client.uid, time.Now().Unix())
		sign := create_signature([]byte(message))
		message += fmt.Sprintf("&reqsign=%d\n", sign)

		length, err := client.conn.Write([]byte(message))
		if err != nil {
			log.Printf("Failed to write heartbeat message: %s\n", err.Error())
		} else if length != len(message) {
			log.Printf("Invalid message write return length %d != %d\n", length, len(message))
		}
	}
}

func (client *LetterClient) composeLetterMessage() string {
	send_letter_message := fmt.Sprintf("cmd=ap_send_letter&uid=%d&touid=%d&type=text&content=%s&msg_id=%s&timestamp=%d",
		client.uid, *receiver_uid, url.QueryEscape(*letter_content), "xxx", time.Now().Unix())
	sign := create_signature([]byte(send_letter_message))
	send_letter_message += fmt.Sprintf("&reqsign=%d\n", sign)
	return send_letter_message
}

func (client *LetterClient) sendLetter() bool {
	message := client.composeLetterMessage()
	log.Printf("Client %d send letter %s\n", client.uid, message)

	if ok := client.writeMessage([]byte(message)); !ok {
		log.Printf("Failed to send letter message\n")
		return false
	}
	data, err := client.readMessage()
	if err != nil {
		log.Printf("Failed to read send letter response: %s\n", err.Error())
		return false
	}
	log.Printf("Client %d send letter response: %s\n", client.uid, string(data))

	/*
		send_letter_response := struct {
			Type   string `json:"type"`
			Flag   string `json:"flag"`
			MsgId  string `json:"msg_id"`
			ToUid  uint32 `json:"touid"`
			Error  uint32 `json:"error"`
			ErrMsg string `json:"errmsg"`
		}{}
		err = json.Unmarshal(data, &send_letter_response)
		if err != nil {
			log.Printf("Failed to unmarshal send letter response : %s\n", err.Error())
			return false
		}
		if send_letter_response.Error != 0 {
			log.Printf("Invalid send letter response: %s\n", send_letter_response.ErrMsg)
			return false
		}
		if send_letter_response.Type != "ap_send_letter" {
			log.Printf("Invalid send letter response type: %s\n", send_letter_response.Type)
			return false
		}
	*/
	return true
}

func (client *LetterClient) runClient() bool {
	if ok := client.getServerInfo(); !ok {
		log.Printf("Failed to get server info\n")
		return false
	}
	if ok := client.doLogin(); !ok {
		log.Printf("Failed to login server\n")
		return false
	}

	go client.sendHeartbeat()

	for {
		<-client.sync_chan
		if ok := client.sendLetter(); !ok {
			log.Printf("Failed to send letter to server\n")
			break
		}
	}
	return true
}
