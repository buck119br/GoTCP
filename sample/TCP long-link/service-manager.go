package main

import (
	"container/list"
	"flag"
	"kuaiyu-libs/http-query"
	"net"
	"sync"
	"time"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
)

type MatchItem struct {
	connector *ClientConnector
	timestamp int64
}

type ServiceManager struct {
	client_mutex  sync.Mutex
	online_client map[uint32]*ClientConnector

	letter_mutex   sync.Mutex
	offline_letter map[uint32]*UserOfflineLetter
	system_letters  *list.List
	last_del	int64

	c2m_chan  chan interface{}
	redis_chan  chan interface{}
}

var client_listen_address = flag.String("client_listen_address", "0.0.0.0:9911", "Client connection listen address.")

//var request_listen_address = flag.String("request_listen_address", "8.8.8.8:9922", "Server request listen address.")
var request_listen_address = flag.String("request_listen_address", "127.0.0.1:9922", "Server request listen address.")
var client_heartbeat_timeout = flag.Int64("client_heartbeat_timeout", 5000, "Client heartbeat timeout.")
var system_letter_timeout = flag.Int64("system_letter_timeout", SYSTEM_LETTER_LINGER, "system public letter linger time")

func newServiceManager() *ServiceManager {

	manager := &ServiceManager{
		online_client:  make(map[uint32]*ClientConnector, 10000),
		offline_letter: make(map[uint32]*UserOfflineLetter, 10000),
		system_letters:  list.New(),
		last_del:	time.Now().Unix(),
		c2m_chan:       make(chan interface{}, 10000),
		redis_chan:       make(chan interface{}, 10000),
	}
	return manager
}


func (manager *ServiceManager) runServiceManager() {

	rdx := newRedisAccessor(manager.redis_chan)
	go rdx.runRedisAccessor()

	manager.loadOfflineMessages()

	go manager.handleClientConnection()
	go manager.handleClientRequest()
	go manager.runRequestHandler()
	go manager.checkTimeoutClient()
//TODO

	go func() {
        	log.Println(http.ListenAndServe("localhost:6060", nil)) 
	}()


	for {
		time.Sleep(1000 * time.Second)
	}
}

func (manager *ServiceManager) loadOfflineMessages() {

	ctx := &LoadMessageCtx{
		uid2letters: manager.offline_letter,
		linger: manager.system_letters,
	}
	
	ctx.wg.Add(1)
	manager.redis_chan <- ctx
	ctx.wg.Wait()

	net_logger.Printf("[Uid(1000000) init lingers]: has %d letters\n", manager.system_letters.Len())
}

func (manager *ServiceManager) handleClientConnection() {
	listen_address, err := net.ResolveTCPAddr("tcp", *client_listen_address)
	if err != nil {
		net_logger.Fatalf("Error: Failed to resolve %s err: %s", *client_listen_address, err.Error())
	}
	listen_socket, err := net.ListenTCP("tcp", listen_address)
	if err != nil {
		net_logger.Fatalf("Error: Failed to listen on %s err: %s", *client_listen_address, err.Error())
	}
	defer listen_socket.Close()

	net_logger.Printf("Start accept client connection on %s", *client_listen_address)

	for {
		conn, err := listen_socket.AcceptTCP()
		if err != nil {
			net_logger.Printf("Failed to accept new tcp connection: %s", err.Error())
			continue
		}

		net_logger.Printf("New connection from %s", conn.RemoteAddr().String())

		conn.SetNoDelay(true)
		conn.SetKeepAlive(true)
		online_client := newClientConnector(conn, manager.c2m_chan, manager.redis_chan)
		go online_client.runClientProcessor()
	}
}

func (manager *ServiceManager) handleClientRequest() {
	for item := range manager.c2m_chan {
		if request, ok := item.(*ClientLoginRequest); ok {
			manager.handleClientLoginRequest(request)
		} else if request, ok := item.(*ClientLogoutRequest); ok {
			manager.handleClientLogoutRequest(request)
		} else if letter, ok := item.(*UserLetterMessage); ok {
			manager.handleUserLetterMessage(letter)
		} else if ctx, ok := item.(*DelAckedCtx); ok {
			manager.handleDeleteAckedLetter(ctx)
		} 
	}
}

func (manager *ServiceManager) handleClientLoginRequest(request *ClientLoginRequest) {
	manager.client_mutex.Lock()
	defer manager.client_mutex.Unlock()

	net_logger.Printf("[Uid(%d) LOGIN]: Start now!! ", request.connector.client.uid)

	new_connector := request.connector
	old_connector, ok := manager.online_client[new_connector.client.uid]
	if ok {
		net_logger.Printf("[Uid(%d) DUP Conn]:  REPLACED with new one", old_connector.client.uid)
	}

	//manager Add letters filed !!
	letterlist, ok := manager.offline_letter[new_connector.client.uid]
	if !ok {
		letterlist = &UserOfflineLetter{uid: new_connector.client.uid, letter_list: list.New()}
		manager.offline_letter[new_connector.client.uid] = letterlist
	}

	manager.online_client[new_connector.client.uid] = new_connector
	new_connector.m2c_chan <- &ManagerLoginResponse{succ: true, error: ERROR_SUCCESS}

	manager.flushClientOfflineLetters(new_connector)
}

func (manager *ServiceManager) flushClientOfflineLetters(connector *ClientConnector) {

	offline_letter := manager.popUserOfflineLetter(connector.client.uid, connector.client.smtime)
	if offline_letter != nil {
		net_logger.Printf("[Uid(%d) flush offline]: has %d letters\n", 
				  connector.client.uid, offline_letter.letter_list.Len())

		for e := offline_letter.letter_list.Front(); e != nil; e = e.Next() {
			if user_letter, ok := e.Value.(*UserLetterMessage); ok {
				connector.m2c_chan <- user_letter
			} else if system_letter, ok := e.Value.(*SystemLetterMessage); ok {
				connector.m2c_chan <- system_letter
			} else {
				net_logger.Printf("[Uid(%d) flush offlines]: Unknown letters\n", 
						  connector.client.uid)
			}
		}

	}
}

func (manager *ServiceManager) handleClientLogoutRequest(request *ClientLogoutRequest) {
	manager.client_mutex.Lock()
	defer manager.client_mutex.Unlock()

	net_logger.Printf("Manager receive logout request: uid=%d", request.connector.client.uid)

	manager.removeClientConnector(request.connector)
	request.connector.m2c_chan <- &ManagerLogoutResponse{padding: 0}
}

func (manager *ServiceManager) handleUserLetterMessage(letter *UserLetterMessage) {

	manager.saveUserMessage(letter.To_user.Uid, letter)
	if manager.sendMessageToClient(letter.To_user.Uid, letter) == false {
		net_logger.Printf("[Uid(%d) recv letter]: NOT online, so try next time", letter.To_user.Uid)
	}
}

func (manager *ServiceManager) handleDeleteAckedLetter(ctx *DelAckedCtx) {

	net_logger.Printf("[Uid(%d),letter ACK]: to delete Msgid(%s)", ctx.uid, ctx.msgid)
	found := false
	letters, ok := manager.offline_letter[ctx.uid]
	if ok {
		net_logger.Printf("[Uid(%d), offline]: has %d letters", ctx.uid, letters.letter_list.Len())
		for it := letters.letter_list.Front(); it != nil; it = it.Next() {
			net_logger.Println("[Del Ack] letters Walk:", it.Value, "\n\n")

			if userletter, ok := it.Value.(*UserLetterMessage); ok {
				//net_logger.Printf("[Uid(%d),loop offline]: this Msgid(%s)", ctx.uid, userletter.Msg_id)
				if userletter.Msg_id == ctx.msgid {
					found = true
					letters.letter_list.Remove(it)
					//remove the fileds Added
					userletter.To_user.Nickname = "" 
					userletter.To_user.Face_s = ""

					ctx := &DeleteLetterCtx {
						letter: userletter, 
					}
					manager.redis_chan <- ctx
					break
				}
			} else {
				net_logger.Printf("[Uid(%d),letter ACK]: not User letter", ctx.uid)
			}
		}

		if letters.letter_list.Len() == 0 {
			delete(manager.offline_letter, ctx.uid)
			net_logger.Printf("[Uid(%d),letter ACK]: O letter, so delete container", ctx.uid)
		}
	} else {
		net_logger.Printf("[Uid(%d),letter ACK]: has no offline letter", ctx.uid)
	}

	if found {
		net_logger.Printf("[Uid(%d)  letter ACK]: successed !! and msgid(%s)", ctx.uid, ctx.msgid)
	} else {
		net_logger.Printf("[Uid(%d): letter Ack]: cannot find, maybe public sysletter", ctx.uid)
	}
}

func (manager *ServiceManager) sendMessageToClient(uid uint32, message interface{}) bool {
	manager.client_mutex.Lock()
	defer manager.client_mutex.Unlock()

	connector, ok := manager.online_client[uid]
	if ok {
		connector.m2c_chan <- message
	} 
	return ok
}

func (manager *ServiceManager) removeClientConnector(connector *ClientConnector) {
	if c, ok := manager.online_client[connector.client.uid]; ok {
		if c == connector {
			net_logger.Printf("Delete connector for client %d from map", connector.client.uid)
			delete(manager.online_client, connector.client.uid)
		}
	}
}

func (manager *ServiceManager) saveUserMessage(uid uint32, message interface{}) {
	manager.letter_mutex.Lock()
	defer manager.letter_mutex.Unlock()

	offline_letter, ok := manager.offline_letter[uid]
	if !ok {
		offline_letter = &UserOfflineLetter{uid: uid, letter_list: list.New()}
		manager.offline_letter[uid] = offline_letter
	}

	offline_letter.letter_list.PushBack(message)

	letter := &StoreLetterCtx{
		letter: message,
	}
	manager.redis_chan <- letter
	net_logger.Printf("[Uid(%d) SAVE letter]: send request to Redis\n", uid)

}

func (manager *ServiceManager) saveSystemLingerMessage(message *SystemLingerMessage) {

	manager.system_letters.PushBack(message)
	letter := &StoreLetterCtx{
		letter: message,
	}
	manager.redis_chan <- letter
	net_logger.Printf("[Uid(1000000) save linger]: sent a store request, content=%s\n", message.Msg_content)
}

func (manager *ServiceManager) popUserOfflineLetter(uid uint32, smtime int64) *UserOfflineLetter {
	
	manager.letter_mutex.Lock()
	defer manager.letter_mutex.Unlock()

	offletter, ok := manager.offline_letter[uid]

	letters := manager.popLingerSysletter(uid, smtime)
	if letters.Len() > 0 {
		if !ok {
			offletter = &UserOfflineLetter{uid: uid, letter_list: list.New()}
		}
		offletter.letter_list.PushBackList(letters)
		net_logger.Printf("[Uid(%d) pop offline]: has %d letters\n", uid, offletter.letter_list.Len())
	} else {
		net_logger.Printf("[Uid(%d) pop offline]: has no letters\n", uid)
	}


	return offletter
}


func (manager *ServiceManager) popLingerSysletter(uid uint32, smtime int64) *list.List {
	

	letters := list.New()
	now := time.Now().Unix()
	expired := false
	delcnt := 0
	if now > manager.last_del + *system_letter_timeout*3 {
		expired = true
		manager.last_del = now
	}

	for it := manager.system_letters.Front(); it != nil; {
		if lletter, ok := it.Value.(*SystemLingerMessage); ok {
			if expired {
				if now > lletter.Timestamp + *system_letter_timeout {
					old := it
					it = it.Next()
					manager.system_letters.Remove(old)
					delcnt = delcnt + 1
					continue
				} else {
					expired = false
				}
			}

			if lletter.Timestamp > smtime {
				sysletter :=  &SystemLetterMessage{
						To_user : UserInfo{Uid:uid,},
						Msg_type:  lletter.Msg_type,
						Msg_content: lletter.Msg_content,
						Timestamp:   lletter.Timestamp,
				}

				letters.PushBack(sysletter)
			}
		}

		it = it.Next() 
	}
	
	if delcnt > 0 {
		ctx := &DelExpiredCtx {
			delcnt:delcnt,
		}

		manager.redis_chan <- ctx 
		net_logger.Printf("[Uid(%d) poplinger]: Trigger DelExpired %d letters to redis\n", uid, delcnt)
	}
	
	net_logger.Printf("[Uid(%d) poplinger]: gathered cnt(%d) untouched system Message\n", uid, letters.Len())

	return letters
}



func (manager *ServiceManager) checkTimeoutClient() {
	for {
		manager.client_mutex.Lock()
		for _, connector := range manager.online_client {
			if connector.hbtime+*client_heartbeat_timeout < time.Now().Unix() {
				net_logger.Printf("Client %d heartbeat is timeout!", connector.client.uid)
				connector.m2c_chan <- &ClientHbTimeoutNotify{padding: 0}
			}
		}
		manager.client_mutex.Unlock()
		time.Sleep(time.Second)
	}
}

func (manager *ServiceManager) runRequestHandler() {
	listen_address, err := net.ResolveUDPAddr("udp", *request_listen_address)
	if err != nil {
		net_logger.Fatal("Error: Failed to resolve address %s : %s", *request_listen_address, err.Error())
	}
	listen_socket, err := net.ListenUDP("udp", listen_address)
	if err != nil {
		net_logger.Fatal("Error: Failed to listen on address %s : %s", *request_listen_address, err.Error())
	}
	for {
		request := make([]byte, 10240)

		length, address, err := listen_socket.ReadFromUDP(request)
		if err != nil {
			net_logger.Printf("Error: Failed to read udp request : %s", err.Error())
			continue
		}
		request = request[0:length]

		net_logger.Printf("RequsetHandler Message: %s\n", string(request))

		go func(request_message []byte, client_address *net.UDPAddr) {
			response_message := manager.handleServerRequest(request_message)
			listen_socket.WriteToUDP(response_message, client_address)
		}(request, address)
	}
}


func (manager *ServiceManager) handleServerRequest(message []byte) []byte {
	request, err := http_query.NewHttpQuery(string(message))
	if err != nil {
		net_logger.Printf("Error: Failed to parse request %s : %s", string(message), err.Error())
		return makeCommonResponse(CMD_UNKNOWN, "flag", ERROR_INVALID_REQUEST, "invalid request")
	}

	var response []byte = nil
	switch request.GetParamDef("cmd", "") {
	case CMD_PRIVATE_MESSAGE:
		response = manager.handlePushPrivateMessage(request)
	case CMD_PUBLIC_MESSAGE:
		response = manager.handlePushPublicMessage(request)
	case CMD_FORWARD_MESSAGE:
		response = manager.handleForwardGiftMessage(request)
	}
	return response
}

func (manager *ServiceManager) handlePushPrivateMessage(request *http_query.HttpQuery) []byte {
	cmd := request.GetParamDef("cmd", "")
	from_uid := uint32(request.GetUintParamDef("fromuid", 0))
	to_uid := uint32(request.GetUintParamDef("touid", 0))
	msg_type := request.GetParamDef("type", "")
	msg_content := request.GetParamDef("content", "")
	follower := uint32(request.GetUintParamDef("by_follower", 0))
	from_name := request.GetParamDef("fromname", " ")

	if to_uid == 0 || len(msg_content) == 0 || msg_type != MSG_TYPE_TEXT {
		net_logger.Printf("Invalid request message %s", request.GetRawMessage())
		return makeCommonResponse(cmd, "flag", ERROR_INVALID_REQUEST, "Invalid request")
	}

	from_user := UserInfo {
		Uid : from_uid,
		Nickname : from_name,
		Face_s : makeUserSmallFaceUrl(from_uid),
	}

	system_letter := &UserLetterMessage{
		From_user:    from_user,
		To_user:      UserInfo{ Uid: to_uid },
		Msg_type:     "system",
		Msg_id:       fmt.Sprintf("%d_%d", from_uid, time.Now().Unix()),
		Msg_category: "text",
		Timestamp:    time.Now().Unix(),
		Msg_content:  msg_content,
		By_follower:  (follower == 1), 
	}


	manager.saveUserMessage(to_uid, system_letter)

	if manager.sendMessageToClient(to_uid, system_letter) == false {
		net_logger.Printf("[Uid(%d) recv letter]: NOT online So try next time", to_uid)
	}

	return makeCommonResponse(cmd, "flag", ERROR_SUCCESS, "Success")
}

func (manager *ServiceManager) handlePushPublicMessage(request *http_query.HttpQuery) []byte {
	cmd := request.GetParamDef("cmd", "")
	msg_type := request.GetParamDef("type", "")
	msg_content := request.GetParamDef("content", "")
	from_uid := uint32(request.GetUintParamDef("fromuid", 0))
	from_name := request.GetParamDef("fromname", " ")

	if msg_type != MSG_TYPE_TEXT || len(msg_content) == 0 {
		net_logger.Printf("Invalid request message %s", request.GetRawMessage())
		return makeCommonResponse(cmd, "flag", ERROR_INVALID_REQUEST, "invalid request")
	}

	// Push to all online client.
	// TODO: refine this code later.

	from_user := UserInfo {
		Uid : from_uid,
		Nickname : from_name,
		Face_s : makeUserSmallFaceUrl(from_uid),
	}

	manager.client_mutex.Lock()
	for uid, connector := range manager.online_client {
		system_letter := &UserLetterMessage{
			From_user: from_user,
			To_user:  UserInfo{Uid: uid},
			Msg_type: "system",
			Msg_id: fmt.Sprintf("%d_%d", from_uid, time.Now().Unix()),
			Msg_category: "text",
			Timestamp:  time.Now().Unix(),
			Msg_content: msg_content,
			By_follower: true, 
		}

		connector.m2c_chan <- system_letter 
	}
	manager.client_mutex.Unlock()

	linger :=  &SystemLingerMessage{
			Msg_type:    msg_type,
			Msg_content: msg_content,
			Timestamp:   time.Now().Unix(),
	}
	manager.saveSystemLingerMessage(linger)
	//TODO
	return makeCommonResponse(cmd, "flag", ERROR_SUCCESS, "Success")
}

func (manager *ServiceManager) handleForwardGiftMessage(request *http_query.HttpQuery) []byte {
	cmd := request.GetParamDef("cmd", "")
	from_uid := uint32(request.GetUintParamDef("from", 0))
	to_uid := uint32(request.GetUintParamDef("to", 0))
	gift_id := uint32(request.GetUintParamDef("gid", 0))
	follower := uint32(request.GetUintParamDef("by_follower", 0))
	from_name := request.GetParamDef("from_name", " ")

	if to_uid == 0 || from_uid == 0 || gift_id == 0 {
		net_logger.Printf("Invalid request message %s", request.GetRawMessage())
		return makeCommonResponse(cmd, "flag", ERROR_INVALID_REQUEST, "Invalid request")
	}

	pgift, err := get_gift_info(gift_id)
	if err != nil {
		net_logger.Printf("Invalid request message %s", request.GetRawMessage())
		return makeCommonResponse(cmd, "flag", ERROR_INVALID_REQUEST, err.Error())
	}


	from_user := UserInfo {
		Uid : from_uid,
		Nickname : from_name,
		Face_s : makeUserSmallFaceUrl(from_uid),
	}

	gift_letter := &UserLetterMessage{
		From_user: from_user,
		To_user:  UserInfo{Uid: to_uid},
		Msg_type: "system",
		Msg_id: fmt.Sprintf("%d_%d", from_uid, time.Now().Unix()),
		Msg_category: "gift",
		Timestamp:  time.Now().Unix(),
		Gift_info: *pgift, 
		By_follower: (follower == 1),
	}

	net_logger.Printf("%s: from(%d) to (%d), giftinfo(%s)\n", cmd, from_uid, to_uid, pgift.Image)

	manager.saveUserMessage(to_uid, gift_letter)

	if manager.sendMessageToClient(to_uid, gift_letter) == false {
		net_logger.Printf("[Uid(%d) recv letter]: NOT online, so try next time", to_uid)
	}

	return makeCommonResponse(cmd, "flag", ERROR_SUCCESS, "Success")
}

