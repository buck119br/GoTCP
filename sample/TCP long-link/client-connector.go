package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"kuaiyu-libs/http-query"
	"net"
	"strings"
	"time"
)

type ClientInfo struct {
	uid      uint32
	nickname string
	gender   uint32
	platform string
	version  string
	device   string
	starttm  uint64
	smtime   int64 // same as ClientConnector
}

type ClientConnector struct {
	conn   *net.TCPConn
	reader *bufio.Reader
	client *ClientInfo

	hbtime    int64 // Last heatbeat message timestamp
	smtime    int64
	need_stop bool

	c2m_chan  chan<- interface{} // client to manager channel
	m2c_chan  chan interface{}   // manager to client channel
	sync_chan chan int
}

var client_heartbeat_interval = flag.Int("client_heartbeat_interval", 5, "Client heartbeat interval.")

func newClientConnector(conn *net.TCPConn, c2m_chan chan<- interface{}, ch chan<- interface{}) *ClientConnector {
	client_connector := &ClientConnector{
		conn:      conn,
		reader:    bufio.NewReader(conn),
		client:    nil,
		hbtime:    0,
		need_stop: false,
		c2m_chan:  c2m_chan,
		m2c_chan:  make(chan interface{}, 1000),
		sync_chan: make(chan int, 2),
	}
	return client_connector
}

func destroyClientConnector(connector *ClientConnector) {
	connector.conn.Close()

	//connector.client = nil
	//connector.c2m_chan = nil
	close(connector.m2c_chan)
}

func (connector *ClientConnector) checkLoginRequest() bool {
	connector.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	defer connector.conn.SetReadDeadline(time.Time{})

	net_logger.Printf("Check login message for connection %s", connector.connInfo())

	message, err := connector.reader.ReadString('\n')
	if err != nil {
		net_logger.Printf("Error: Failed to read login message from %s", connector.connInfo())
		return false
	}
	message = strings.Trim(message, "\n")
	net_logger.Printf("Client login message %s from %s", message, connector.connInfo())

	request, err := http_query.NewHttpQuery(message)
	if err != nil {
		net_logger.Printf("Error: Failed to parse login message from %s, err: %s", connector.connInfo(), err.Error())
		return false
	}


	if request.GetParamDef("cmd", "") != CMD_LOGIN {
		net_logger.Printf("Error: First command is not login for connection: %s", connector.connInfo())
		return false
	}

	reqsign := uint32(request.GetUintParamDef("reqsign", 0))
	if reqsign == 0 {
		net_logger.Printf("[client checkin]: NO REQSIGN parameter in request string")
		return false
	} else  {
		idx := strings.Index(message, "&reqsign")
		sign := create_signature([]byte(message[:idx]))
		if reqsign != sign {
			net_logger.Printf("[client checkin]: Checking reqeust signature FAILED, as sign=%d", sign)
			return false
		}
	}

	uid := uint32(request.GetUintParamDef("uid", 0))

	user_sig := request.GetParamDef("sig", "")
	if len(user_sig) == 0 { 
		net_logger.Printf("[client checkin]: NO SIG parameter in request string")
		return false
	} else {
		///*
		user, err := check_user_sign(uid, user_sig) 
		if err != nil {
			net_logger.Printf("[Uid(%d) checkSign]: Checking User signature FAILED, and %s", uid, err.Error())
			return false
		} else {
			net_logger.Printf("[Uid(%d) check usersign]:  Valid sign, and fetched Uid=%d!", uid, user.Uid)
		}
		//*/
	}

	connector.client = &ClientInfo{
		uid:	  uid, 
		nickname: request.GetParamDef("nickname", ""),
		gender:   uint32(request.GetUintParamDef("gender", 0)),
		platform: request.GetParamDef("platform", ""),
		version:  request.GetParamDef("ver", ""),
		device:   request.GetParamDef("device", ""),
		smtime:   request.GetIntParamDef("smtime", 0),
	}
	connector.hbtime = time.Now().Unix()
	connector.smtime = request.GetIntParamDef("smtime", 0)

	connector.c2m_chan <- &ClientLoginRequest{connector: connector}
	return true
}

func (connector *ClientConnector) runClientProcessor() {
	defer destroyClientConnector(connector)

	if ok := connector.checkLoginRequest(); !ok {
		net_logger.Printf("Error: Failed to check login message for client %s", connector.connInfo())
		return
	}
	net_logger.Printf("New client information: %s", connector.clientInfo())

	go connector.runConnectorReadProcessor()
	go connector.runConnectorWriteProcessor()

	k := <-connector.sync_chan
	if k == 0 {
		connector.m2c_chan <- &CloseConnectorRequest{padding: 0}
	} else {
		connector.conn.Close()
	}
	<-connector.sync_chan

	connector.c2m_chan <- &ClientLogoutRequest{connector: connector}

	for item := range connector.m2c_chan {
		if _, ok := item.(*ManagerLogoutResponse); ok { 
			net_logger.Printf("[Uid(%d) Logout] recv logout response from manager", connector.client.uid)
			break 
		}
	}

	net_logger.Printf("[Uid(%d) Logout] It's  now stopped", connector.client.uid)
}

func (connector *ClientConnector) runConnectorReadProcessor() {
	net_logger.Printf("[Uid(%d) readProcess] Start now!", connector.client.uid)

	for {
		message, err := connector.reader.ReadString('\n')
		if err != nil {
			net_logger.Printf("Error: Failed to read from client %d, err: %s", connector.client.uid, err.Error())
			break
		} else {
			message = strings.Trim(message, "\n")
			//net_logger.Printf("Read message from client %d : %s", connector.client.uid, message)
			connector.handleClientMessage(message)
		}
	}
	connector.sync_chan <- 0

	net_logger.Printf("[Uid(%d) readProcess]: Stop now !!", connector.client.uid)
}

func (connector *ClientConnector) runConnectorWriteProcessor() {
	net_logger.Printf("[Uid(%d) writeProcess] Start now!", connector.client.uid)

	for item := range connector.m2c_chan {
		if request, ok := item.(*CloseConnectorRequest); ok {
			connector.handleCloseConnectorRequest(request)
		} else if response, ok := item.(*ManagerLoginResponse); ok {
			connector.handleManagerLoginResponse(response)
		} else if notify, ok := item.(*ClientHbTimeoutNotify); ok {
			connector.handleManagerHbTimeoutNotify(notify)
		} else if notify, ok := item.(*ClientKickedNotify); ok {
			connector.handleManagerKickClientNotify(notify)
		} else if letter, ok := item.(*UserLetterMessage); ok {
			connector.handleUserLetterMessage(letter)
		} else if letter, ok := item.(*SystemLetterMessage); ok {
			connector.handleSystemLetterMessage(letter)
		} else {
			net_logger.Printf("[Uid(%d) UNKNOWN request]: writer DROP this request\n", connector.client.uid)
		}
		if connector.need_stop {
			break
		}
	}
	connector.sync_chan <- 1

	net_logger.Printf("[Uid(%d) writeProcess]: STOP now!!", connector.client.uid)
}

func (connector *ClientConnector) handleClientMessage(message string) {
	request, err := http_query.NewHttpQuery(message)
	if err != nil {
		net_logger.Printf("Error: Invalid client message %s, err: %s", message, err.Error())
		return
	}

	badsign := false
	reqsign := uint32(request.GetUintParamDef("reqsign", 0))
	if reqsign == 0 {
		badsign = true
		net_logger.Printf("[letter ACK]: NO REQSIGN parameter in request string")
	} else  {
		idx := strings.Index(message, "&reqsign")
		sign := create_signature([]byte(message[:idx]))
		if reqsign != sign {
			badsign = true
			net_logger.Printf("[letter ACK]: Checking reqeust signature FAILED, sign=%d", sign)
		}
	}

	switch request.GetParamDef("cmd", "") {
	case CMD_HEARTBEAT:
		connector.handleClientHeartbeatRequest(request, badsign)
	case CMD_SEND_LETTER:
		connector.handleClientSendLetterRequest(request, badsign)
	case CMD_RECV_LETTER:
		connector.handleClientRecvLetterAck(request, badsign)
	default:
		connector.handleUnknownRequest(request)
	}
}

func (connector *ClientConnector) handleClientHeartbeatRequest(request *http_query.HttpQuery, badsign bool) {
	//TODO
	if badsign {
		net_logger.Printf("[letter ACK]: BAD REQSIGN, raw request(%s)", request.GetRawMessage())
		return
	}

	//net_logger.Printf("Client %d heartbeat message received.", connector.client.uid)
	connector.hbtime = time.Now().Unix()
}

func (connector *ClientConnector) handleClientSendLetterRequest(request *http_query.HttpQuery, badsign bool) {

	net_logger.Printf("[Uid(%d) send letter]: START now!!", connector.client.uid)
	net_logger.Printf("%s", request.GetRawMessage())

	isBad := false
	if badsign {
		isBad = true
		net_logger.Printf("[send letter]: BAD REQSIGN, raw request(%s)", request.GetRawMessage())
	}

	cmd := request.GetParamDef("cmd", " ")
	to_uid := uint32(request.GetUintParamDef("touid", 0))
	if to_uid == 0 {
		isBad = true
		net_logger.Printf("[send letter]: Invalid send letter request, touid is 0")
	}

	message_type := request.GetParamDef("type", "")
	if message_type != MSG_TYPE_TEXT {
		isBad = true
		net_logger.Printf("[send letter]: Invalid send letter request, type unknown ")
	}
	message_content := request.GetParamDef("content", "")
	if len(message_content) == 0 {
		isBad = true
		net_logger.Printf("[send letter]: Invalid send letter request, content empty")
	}
	message_id := request.GetParamDef("msg_id", "")
	if len(message_id) == 0 {
		isBad = true
		net_logger.Printf("[send letter]: Invalid send letter request, msgid empty")
	}

	if isBad {
		connector.pushCommonResponse(cmd, message_id, to_uid, ERROR_INVALID_REQUEST)
		net_logger.Printf("[send letter]: raw request info %s", request.GetRawMessage())
		return
	}

	message_timestamp := request.GetIntParamDef("timestamp", 0)
	sender_in_receiver_blacklist, err := isSenderInBlacklistOfReceiver(connector.client.uid, to_uid)
	if err != nil {
		net_logger.Printf("[Uid(%d) send letter]: Failed to check if %d in blacklist of %d, accept it, %s", 
				  connector.client.uid, connector.client.uid, to_uid, err.Error())
	}
	if sender_in_receiver_blacklist {
		net_logger.Printf("[Uid(%d) send letter]: in blacklist of client %d, drop this message.", connector.client.uid, to_uid)
		connector.pushCommonResponse(cmd, message_id, to_uid, ERROR_SUCCESS)
		return
	}
	receiver_follow_sender, err := isReceiverFollowSender(to_uid, connector.client.uid)
	if err != nil {
		net_logger.Printf("[send letter]: Failed to check receiver %d follow sender %d", 
				  to_uid, connector.client.uid)
	}

	from_user := UserInfo {
		Uid : connector.client.uid,
		Nickname : connector.client.nickname,
		Face_s : makeUserSmallFaceUrl(connector.client.uid),
	}

	user_letter := &UserLetterMessage{
		From_user:    from_user,
		To_user:      UserInfo{Uid: to_uid},
		Msg_type:     "personal",
		Msg_id:       message_id,
		Msg_category: "text",
		Timestamp:    message_timestamp,
		Msg_content:  message_content,
		By_follower:  receiver_follow_sender,
	}

	connector.c2m_chan <- user_letter

	connector.pushCommonResponse(cmd, message_id, to_uid, ERROR_SUCCESS)
}

func (connector *ClientConnector) handleClientRecvLetterAck(response *http_query.HttpQuery, badsign bool) {

	if badsign {
		net_logger.Printf("[letter ACK]: BAD REQSIGN, raw request(%s)", response.GetRawMessage())
		return
	}

	msgid := response.GetParamDef("msg_id", "")
	if len(msgid) == 0 {
		net_logger.Printf("Error: Invalid letter ACK response, msgid empty : %s", response.GetRawMessage())
		return 
	}


	ctx := &DelAckedCtx {
		uid: connector.client.uid,
		msgid: msgid,
	}

	connector.c2m_chan <- ctx
	net_logger.Printf("Notice: user(%d) sent a DelAckedCtx to manager", connector.client.uid)

}

func (connector *ClientConnector) handleUnknownRequest(request *http_query.HttpQuery) {
	net_logger.Printf("Error: Unknown client %d request %s.", connector.client.uid, request.GetRawMessage())
}

func (connector *ClientConnector) handleCloseConnectorRequest(request *CloseConnectorRequest) {
	net_logger.Printf("Client %d received close request.", connector.client.uid)
	connector.need_stop = true
}

func (connector *ClientConnector) handleManagerLoginResponse(response *ManagerLoginResponse) {
	login_response := struct {
		Type   string `json:"type"`
		Flag   string `json:"flag"`
		HbTime int    `json:"hbtime"`
		Error  int    `json:"error"`
		ErrMsg string `json:"errmsg"`
	}{
		Type:   CMD_LOGIN,
		Flag:   FLAG_ACK,
		HbTime: *client_heartbeat_interval,
		Error:  response.error,
		ErrMsg: makeErrorMessage(response.error),
	}
	message, err := json.Marshal(login_response)
	if err != nil {
		connector.need_stop = true
		net_logger.Printf("Error: failed to marshal login response: %s", err.Error())
	} else {
		message = append(message, '\n')
		connector.writeMessage(message)
	}

	if !response.succ {
		connector.need_stop = true
	}
}

func (connector *ClientConnector) handleUserLetterMessage(letter *UserLetterMessage) {

	net_logger.Printf("[Uid(%d) send letter]: Writer START to send\n", connector.client.uid)

	letter.To_user.Nickname = connector.client.nickname
	letter.To_user.Face_s = makeUserSmallFaceUrl(connector.client.uid)

	letter_notify := struct {
		Type         string   `json:"type"`
		Flag         string   `json:"flag"`
		Msg_id	     string    `json:"msg_id"`
		MsgType      string   `json:"msg_type"`
		MsgContent   string   `json:"msg_content"`
		FromUser     UserInfo `json:"from_user"`
		ToUser       UserInfo `json:"to_user"`
		FromFollower bool     `json:"from_follower"`
		Msg_category string   `json:"msg_category"`
		Giftinfo     GiftInfo `json:"gift_info"`
		Timestamp    int64    `json:"timestamp"`
	}{
		Type:         NOTIFY_LETTER_MESSAGE,
		Flag:         FLAG_NOTIFY,
		MsgType:      letter.Msg_type,
		MsgContent:   letter.Msg_content,
		FromUser:     letter.From_user,
		ToUser:       letter.To_user,
		FromFollower: letter.By_follower,
		Timestamp:    letter.Timestamp,
		Giftinfo:     letter.Gift_info,
		Msg_id:       letter.Msg_id,
		Msg_category: letter.Msg_category,
	}

	message, err := json.Marshal(letter_notify)
	if err != nil {
		net_logger.Printf("Error: Failed to marshal letter message : %s", err.Error())
	} else {
		message = append(message, '\n')
		connector.writeMessage(message)
	}
}

func (connector *ClientConnector) handleSystemLetterMessage(letter *SystemLetterMessage) {
	letter_notify := &struct {
		Type       string   `json:"type"`
		Flag       string   `json:"flag"`
		MsgType    string   `json:"type"`
		MsgContent string   `json:"content"`
		ToUser     UserInfo `json:"to_user"`
		Timestamp  int64    `json:"timestamp"`
	}{
		Type:       NOTIFY_SYSTEM_MESSAGE,
		Flag:       FLAG_NOTIFY,
		MsgType:    letter.Msg_type,
		MsgContent: letter.Msg_content,
		ToUser: UserInfo{
			Uid:      connector.client.uid,
			Nickname: connector.client.nickname,
			Face_s:   makeUserSmallFaceUrl(connector.client.uid),
		},
		Timestamp: letter.Timestamp,
	}

	message, err := json.Marshal(letter_notify)
	if err != nil {
		net_logger.Printf("Error: Failed to marshal system letter message : %s", err.Error())
	} else {
		message = append(message, '\n')
		connector.writeMessage(message)
		net_logger.Printf("SystemLetter : %s", string(message))
	}
}

func (connector *ClientConnector) handleManagerHbTimeoutNotify(notify *ClientHbTimeoutNotify) {
	hb_timeout_notify := struct {
		Type string `json:"type"`
		Flag string `json:"flag"`
	}{
		Type: NOTIFY_HEARTBEAT_TIMEOUT,
		Flag: FLAG_NOTIFY,
	}
	message, err := json.Marshal(hb_timeout_notify)
	if err != nil {
		net_logger.Printf("Error: Failed to marshal hb timeout message: %s", err.Error())
	} else {
		message = append(message, '\n')
		connector.writeMessage(message)
	}
	connector.need_stop = true
}

func (connector *ClientConnector) handleManagerKickClientNotify(notify *ClientKickedNotify) {
	kick_notify := struct {
		Type string `json:"type"`
		Flag string `json:"flag"`
	}{
		Type: NOTIFY_CLIENT_KICKED,
		Flag: FLAG_NOTIFY,
	}
	message, err := json.Marshal(kick_notify)
	if err != nil {
		net_logger.Printf("Error: Failed  to marshal client kicked message: %s", err.Error())
	} else {
		message = append(message, '\n')
		connector.writeMessage(message)
	}
	connector.need_stop = true
}

func (connector *ClientConnector) pushCommonResponse(cmd string, msgid string, touid uint32, errcode int) {
	error_response := struct {
		Type   string `json:"type"`
		Flag   string `json:"flag"`
		Msgid   string `json:"msg_id"`
		Touid   uint32 `json:"touid"`
		Error  int    `json:"error"`
		ErrMsg string `json:"errmsg"`
	}{
		Type:   cmd,
		Flag:   FLAG_ACK,
		Msgid:  msgid,
		Touid:  touid,
		Error:  errcode,
		ErrMsg: makeErrorMessage(errcode),
	}
	message, err := json.Marshal(error_response)
	if err != nil {
		connector.need_stop = true
		net_logger.Printf("Error: Failed to marshal client response: %s", err.Error())
	} else {
		message = append(message, '\n')
		connector.writeMessage(message)
	}
}

func (connector *ClientConnector) writeMessage(message []byte) {
	//time.Sleep(time.Second*5)
	net_logger.Printf("[Uid(%d) writer]: send message[ %s ]", connector.client.uid, string(message))

	n, err := connector.conn.Write(message)
	if err != nil {
		net_logger.Printf("Error: client %d failed to write message: %s", connector.client.uid, err.Error())
		connector.need_stop = true
	} else if n != len(message) {
		net_logger.Printf("Error: client %d write message length error", connector.client.uid)
		connector.need_stop = true
	}
}

func (connector *ClientConnector) connInfo() string {
	return connector.conn.RemoteAddr().String()
}

func (connector *ClientConnector) clientInfo() string {
	return fmt.Sprintf("uid=%d nickname=%s gender=%d platform=%s version=%s device=%s",
		connector.client.uid, connector.client.nickname, connector.client.gender,
		connector.client.platform, connector.client.version, connector.client.device)
}
