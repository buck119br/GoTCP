package main

const (
	ERROR_SUCCESS = iota
	ERROR_CLIENT_CONFLICT
	ERROR_INVALID_REQUEST
)

const (
	CMD_LOGIN           = "ap_login"
	CMD_HEARTBEAT       = "ap_heartbeat"
	CMD_SEND_LETTER	    = "ap_send_letter"
	CMD_RECV_LETTER     = "ap_letter_ack"
	CMD_PRIVATE_MESSAGE = "push_private_message"
	CMD_PUBLIC_MESSAGE  = "push_public_message"
	CMD_FORWARD_MESSAGE = "forward_gift_message"
	CMD_UNKNOWN	    = "unknow_cmd_request"
)

const (
	NOTIFY_LETTER_MESSAGE    = "ap_letter_msg"
	NOTIFY_SYSTEM_MESSAGE    = "ap_system_msg"
	NOTIFY_HEARTBEAT_TIMEOUT = "ap_heatbeat_timeout"
	NOTIFY_CLIENT_KICKED     = "ap_client_kicked"
)

const (
	FLAG_ACK    = "ack"
	FLAG_NOTIFY = "notify"
)

const (
	USER_LETTER = iota
	SYSTEM_LETTER
)

const (
	MSG_TYPE_TEXT = "text"
)

const (
	GIFTINFO_HOMEPAGE = "http://test.kuaiyuzhibo.cn/kuaiyu/service/gift.php"
	KUAIYU_HOMEPAGE = "http://test.kuaiyuzhibo.cn/kuaiyu/service"
)

const (
	KuaiyuSuperUid = 1000000
)

const (
	SYSTEM_LETTER_LINGER = 3*24*60*60
	//SYSTEM_LETTER_LINGER = 5*30
)

const (
	OFFLINE_LETTER_PREFIX = "OLP:"
	SYSTEM_LINGER_PREFIX = "SLP:"
)
