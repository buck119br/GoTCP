package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"encoding/base64"
	"flag"
	"fmt"
	"hash/crc32"
	"net"
	"net/http"
	"time"
	"errors"
	"io/ioutil"
	"net/url"
)

var blacklist_server = flag.String("blacklist_server", "127.0.0.1:6677", "Black list server address.")
var relation_server = flag.String("relation_server", "127.0.0.1:6688", "Relation server address.")
var giftinfo_homepage = flag.String("giftinfo_homepage", GIFTINFO_HOMEPAGE, "Gift info's Homepage")
var kuaiyu_homepage = flag.String("kuaiyu_homepage", KUAIYU_HOMEPAGE, "kuaiyu API service 's Homepage")

func makeErrorMessage(errcode int) string {
	var errmsg string
	switch errcode {
	case ERROR_SUCCESS:
		errmsg = "success"
	case ERROR_CLIENT_CONFLICT:
		errmsg = "client conflict"
	default:
		errmsg = "unknown error"
	}
	return errmsg
}

func makeUserSmallFaceUrl(uid uint32) string {
	prefix := "http://mediacdn.kuaiyuzhibo.cn/liveshow/face"
	key := crc32.ChecksumIEEE([]byte(fmt.Sprintf("liveshow%06d", uid)))
	return fmt.Sprintf("%s/%02d/%02d/%d_s.jpg", prefix, key%100, key/100%100, uid)
}

func makeUdpRequest(server, request string) ([]byte, error) {
	server_address, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, err
	}
	local_address, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	connection, err := net.DialUDP("udp", local_address, server_address)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	connection.Write([]byte(request))
	connection.SetReadDeadline(time.Now().Add(time.Second))

	response := make([]byte, 102400)
	length, err := connection.Read(response)
	if err != nil {
		return nil, err
	}
	response = response[0:length]
	return response, nil
}

func isSenderInBlacklistOfReceiver(sender, receiver uint32) (bool, error) {
	request := fmt.Sprintf("op=contains&table=blacklist&id=%d&value=%d", receiver, sender)

	response, err := makeUdpRequest(*blacklist_server, request)
	if err != nil {
		return false, err
	}

	result_data := struct {
		Result  string `json:"result"`
		Message string `json:"message"`
	}{}
	err = json.Unmarshal(response, &result_data)
	if err != nil {
		return false, err
	}
	return result_data.Message == "yes", nil
}

func isReceiverFollowSender(receiver, sender uint32) (bool, error) {
	request := fmt.Sprintf("op=isfans&from=%d&to=%d", receiver, sender)

	response, err := makeUdpRequest(*relation_server, request)
	if err != nil {
		return false, err
	}

	result_data := &struct {
		Result string `json:"result"`
		Msg    string `json:"msg"`
	}{}
	err = json.Unmarshal(response, result_data)
	if err != nil {
		return false, err
	}
	return result_data.Result == "ok", nil
}

func makeCommonResponse(restype string, flag string, error int, errmsg string) []byte {
	common_response := CommonResponse{
		Type:  restype,
		Flag:  flag,
		Error:  error,
		Errmsg: errmsg,
	}
	response_data, err := json.Marshal(common_response)
	if err != nil {
		return []byte("Failed to marshal response!")
	}
	return response_data
}


func serializeSystemLinger(lletter *SystemLingerMessage) (string, error) {
	var network bytes.Buffer
	enc := gob.NewEncoder(&network)
	err := enc.Encode(lletter)
	if err != nil {
		return "", err
	}

	str := base64.StdEncoding.EncodeToString(network.Bytes())
	return str, nil
}

func serializeSystemLetter(sletter *SystemLetterMessage) (string, error) {
	var network bytes.Buffer
	enc := gob.NewEncoder(&network)
	err := enc.Encode(sletter)
	if err != nil {
		return "", err
	}

	str := base64.StdEncoding.EncodeToString(network.Bytes())
	return str, nil
}

func serializeUserLetter(uletter *UserLetterMessage) (string, error) {
	var network bytes.Buffer
	enc := gob.NewEncoder(&network)
	err := enc.Encode(uletter)
	if err != nil {
		return "", err
	}

	str := base64.StdEncoding.EncodeToString(network.Bytes())
	return str, nil
}


func deserializeLetter(bs []byte) (interface{}, error) {

	data, err := base64.StdEncoding.DecodeString(string(bs))
	if err != nil {
		return nil, err 
	}

	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	uletter := new(UserLetterMessage)
	err = dec.Decode(uletter)
	if err == nil {
		return uletter, nil
	}

	sletter := new(SystemLetterMessage)
	err = dec.Decode(sletter)
	if err == nil {
		return sletter, nil
	}

	lletter := new(SystemLingerMessage)
	err = dec.Decode(lletter)
	if err == nil {
		return lletter, nil
	}

	return nil, errors.New("Deserialize: Unknow Data")
}

func isTheLetter(bs []byte, hint interface{}) (bool) {
	fmt.Printf("enter isTheLetter\n")
	uletter := new(UserLetterMessage)
	buf := bytes.NewBuffer(bs)
	dec := gob.NewDecoder(buf)
	err := dec.Decode(uletter)
	if err == nil {
		if msgid, ok := hint.(string); ok {
			if uletter.Msg_id == msgid {
				return true
			}
		}
	} else {
		sletter := new(SystemLetterMessage)
		err := dec.Decode(sletter)
		if err == nil {
			if timestamp, ok := hint.(int64); ok {
				if sletter.Timestamp == timestamp{
					return true
				}
			}
		}
	} 

	return false
}

func makeLetterKey(uid uint32) string {
	return fmt.Sprintf("%s%d", OFFLINE_LETTER_PREFIX, uid)
}

func makeLingerKey(uid uint32) string {
	return fmt.Sprintf("%s%d", SYSTEM_LINGER_PREFIX, uid)
}

func get_gift_info(giftid uint32) (*GiftInfo, error) {

	url2 := fmt.Sprintf("%s?cmd=gift_info&gid=%d&sysflag=kuaiyu827", *giftinfo_homepage, giftid)

	net_logger.Printf("[Uid(%d) GET giftinfo]: raw url: [%s]", url2)

	req, err := http.NewRequest("GET", url2, nil)
	if err != nil {
		net_logger.Printf("Failed to create http request for url %s: %s", url2, err.Error())
		return nil, errors.New("Failed to create http request")
	}

	client := &http.Client{Timeout: time.Duration(5 * time.Second)}
	res, err := client.Do(req)
	if err != nil {
		net_logger.Printf("Failed to do request for url %s: %s", url2, err.Error())
		return nil, errors.New("Failed to do http request")
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		net_logger.Printf("Failed to read body for url %s: %s", url2, err.Error())
		return nil, errors.New("Failed to read http response body")
	}

	var resp GiftInfoResponse
	err = json.Unmarshal(body, &resp)
	if err != nil {
		net_logger.Printf("Failed to read body for url %s: %s", url2, err.Error())
		return nil, errors.New("Failed to parse giftinfo jsonstring")
	}

	if resp.Error == 0 {
		return &resp.Result, nil
	}

	return nil, errors.New(resp.Errmsg)

}

func check_user_sign(uid uint32, usersig string) (*UserProfile, error) {

	url2 := fmt.Sprintf("%s/server.php?cmd=checkmsgsig&uid=%d&sysflag=kuaiyu827&sig=%s", 
			    *kuaiyu_homepage, uid, url.QueryEscape(usersig))

	net_logger.Printf("[Uid(%d) Check UserSign]: [%s]", uid, url2)
	req, err := http.NewRequest("GET", url2, nil)
	if err != nil {
		net_logger.Printf("Failed to create http request for url %s: %s", url2, err.Error())
		return nil, errors.New("Failed to create http request")
	}

	client := &http.Client{Timeout: time.Duration(5 * time.Second)}
	res, err := client.Do(req)
	if err != nil {
		net_logger.Printf("Failed to do request for url %s: %s", url2, err.Error())
		return nil, errors.New("Failed to do http request")
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		net_logger.Printf("Failed to read body for url %s: %s", url2, err.Error())
		return nil, errors.New("Failed to read http response body")
	}

	var resp UserProfileResponse
	err = json.Unmarshal(body, &resp)
	if err != nil {
		net_logger.Printf("Failed to read body for url %s: %s", url2, err.Error())
		return nil, errors.New("Failed to parse giftinfo jsonstring")
	}

	if resp.Error != 0 {
		return nil, errors.New(resp.Errmsg)
	}

	if info, ok := resp.Result["user"]; ok {
		return &info, nil
	} else {
		return nil, errors.New("parsed but no 'user' key")
	}

}
