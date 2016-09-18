package main

import ( "container/list"
	 "sync"
)

type ClientLoginRequest struct {
	connector *ClientConnector
}

type ManagerLoginResponse struct {
	succ  bool
	error int
}

type ClientLogoutRequest struct {
	connector *ClientConnector
}

type ManagerLogoutResponse struct {
	padding uint32
}

type CloseConnectorRequest struct {
	padding uint32
}

type ClientHbTimeoutNotify struct {
	padding uint32
}

type ClientKickedNotify struct {
	padding uint32
}


type UserInfo struct {
	Uid      uint32 `json:"uid"`
	Nickname string `json:"nickname"`
	Face_s   string `json:"face_s"`
}

/*
type UserLetterMessage2 struct {
	from_user   UserInfo
	to_user     UserInfo
	msg_type    string
	msg_content string
	by_follower bool
	timestamp   int64
}
*/

type GiftInfo struct {
	Id int	   `json:"id"`
	Name string `json:"name"`
	Image string `json:"image"`
}

type UserLetterMessage struct {
	From_user   UserInfo `json:"from_user"`
	To_user     UserInfo `json:"to_user"`
	Msg_id   string	     `json:"msg_id"`
	Msg_type    string `json:"msg_type"`
	Msg_category string `json:"msg_category"`
	Msg_content string `json:"msg_content"`
	Gift_info GiftInfo `json:"gift_info"`
	By_follower bool  `json:"by_follower"`
	Timestamp   int64 `json:"timestamp"`
}

type SystemLetterMessage struct {
	To_user     UserInfo `json:"to_user"`
	Msg_type    string   `json:"msg_type"`
	Msg_id      string   `json:"msg_id"`
	Msg_content string   `json:"msg_content"`
	Timestamp   int64    `json:"timestamp"`
}

type SystemLingerMessage struct {
	Msg_type    string  `json:"msg_type"`
	Msg_content string  `json:"msg_content"`
	Timestamp   int64   `json:"timestamp"`
}

type UserOfflineLetter struct {
	uid         uint32
	//msgid	    string //map
	//redis_val   string //
	letter_list *list.List
}

type LoadMessageCtx struct {
	 wg  sync.WaitGroup
	 uid2letters map[uint32]*UserOfflineLetter
	 linger      *list.List
}


type StoreLetterCtx struct {
	letter  interface{} // msgid/Hbtime
}

type DeleteLetterCtx struct {
	letter  interface{} // msgid/Hbtime
}

type DelExpiredCtx struct {
	delcnt int
}

type DelAckedCtx struct {
	uid   uint32
	msgid string
}

type CommonResponse struct {
	Type  string  `json:"type"`
	Flag  string  `json:"flag"`
	Error  int    `json:"error"`
	Errmsg string `json:"errmsg"`
}


type GiftInfoResponse struct {
	Error  int    `json:"error"`
	Errmsg string `json:"errmsg"`
	Result GiftInfo `json:"result"`
}

type UserProfile struct {
	Uid	        int `json:"uid"`
	//Nickname        string `json:"nickname"`
	//Regdate	        int64 `json:"regdate"`
	//Gender	        int `json:"gender"`
	//Sign	        string `json:"signature"`
	//Favor	        int `json:"favor"`
	//Bandate  	int `json:"bandate"`
	//Notified	int `json:"notified"`
	//Expnum  	int `json:"expnum"`
//	Level   	int `json:"level"`
//	Avator	        int `json:"avator"`
}
type UserProfileResponse struct {
	Error  int    `json:"error"`
	Errmsg string `json:"errmsg"`
	Result map[string]UserProfile `json:"result"`
}


