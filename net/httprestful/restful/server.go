package restful

import (
	. "DNA/common/config"
	"DNA/common/log"
	. "DNA/net/httprestful/common"
	Err "DNA/net/httprestful/error"
	"DNA/net/httpwebsocket"
	"context"
	"crypto/tls"
	"encoding/json"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type handler func(map[string]interface{}) map[string]interface{}
type Action struct {
	sync.RWMutex
	name    string
	handler handler
}
type restServer struct {
	router           *Router
	listener         net.Listener
	server           *http.Server
	postMap          map[string]Action
	getMap           map[string]Action
	checkAccessToken func(auth_type, access_token string) (string, int64, interface{})
}

const (
	Api_Getconnectioncount           = "/api/v1/node/connectioncount"
	Api_Getblockbyheight             = "/api/v1/block/details/height/:height"
	Api_Getblockbyhash               = "/api/v1/block/details/hash/:hash"
	Api_Getblockheight               = "/api/v1/block/height"
	Api_Getblockhash                 = "/api/v1/block/hash/:height"
	Api_Gettransaction               = "/api/v1/transaction/:hash"
	Api_Getasset                     = "/api/v1/asset/:hash"
	Api_GetUnspendOutput             = "/api/v1/asset/unspendoutput"
	Api_Restart                      = "/api/v1/restart"
	Api_SendRawTransaction           = "/api/v1/transaction"
	Api_SendCustomRecordTxByTransfer = "/api/v1/custom/transaction/record"
	Api_OauthServerAddr              = "/api/v1/config/oauthserver/addr"
	Api_NoticeServerAddr             = "/api/v1/config/noticeserver/addr"
	Api_NoticeServerState            = "/api/v1/config/noticeserver/state"
	Api_WebsocketState               = "/api/v1/config/websocket/state"
)

func InitRestServer(checkAccessToken func(string, string) (string, int64, interface{})) ApiServer {
	rt := &restServer{}
	rt.checkAccessToken = checkAccessToken

	rt.router = NewRouter()
	rt.registryMethod()
	rt.initGetHandler()
	rt.initPostHandler()
	return rt
}

func (rt *restServer) Start() error {
	if Parameters.HttpRestPort == 0 {
		log.Fatal("Not configure HttpRestPort port ")
		return nil
	}

	tlsFlag := false
	if tlsFlag || Parameters.HttpRestPort%1000 == 443 {
		var err error
		rt.listener, err = rt.initTlsListen()
		if err != nil {
			log.Error("Https Cert: ", err.Error())
			return err
		}
	} else {
		var err error
		rt.listener, err = net.Listen("tcp", ":"+strconv.Itoa(Parameters.HttpRestPort))
		if err != nil {
			log.Fatal("net.Listen: ", err.Error())
			return err
		}
	}
	rt.server = &http.Server{Handler: rt.router}
	err := rt.server.Serve(rt.listener)

	if err != nil {
		log.Fatal("ListenAndServe: ", err.Error())
		return err
	}

	return nil
}

func (rt *restServer) registryMethod() {

	getMethodMap := map[string]Action{
		Api_Getconnectioncount: {name: "getconnectioncount", handler: GetConnectionCount},
		Api_Getblockbyheight:   {name: "getblockbyheight", handler: GetBlockByHeight},
		Api_Getblockbyhash:     {name: "getblockbyhash", handler: GetBlockByHash},
		Api_Getblockheight:     {name: "getblockheight", handler: GetBlockHeight},
		Api_Getblockhash:       {name: "getblockhash", handler: GetBlockHash},
		Api_Gettransaction:     {name: "gettransaction", handler: GetTransactionByHash},
		Api_Getasset:           {name: "getasset", handler: GetAssetByHash},
		Api_GetUnspendOutput:   {name: "getunspendoutput", handler: GetUnspendOutput},
		Api_OauthServerAddr:    {name: "getoauthserveraddr", handler: GetOauthServerAddr},
		Api_NoticeServerAddr:   {name: "getnoticeserveraddr", handler: GetNoticeServerAddr},
		Api_Restart:            {name: "restart", handler: rt.Restart},
	}

	setWebsocketState := func(cmd map[string]interface{}) map[string]interface{} {
		resp := ResponsePack(Err.SUCCESS)
		startFlag, ok := cmd["Open"].(bool)
		if !ok {
			resp["Error"] = Err.INVALID_PARAMS
			return resp
		}
		if b, ok := cmd["PushBlock"].(bool); ok {
			httpwebsocket.SetWsPushBlockFlag(b)
		}
		if wsPort, ok := cmd["Port"].(float64); ok && wsPort != 0 {
			Parameters.HttpWsPort = int(wsPort)
		}
		if startFlag {
			httpwebsocket.ReStartServer()
		} else {
			httpwebsocket.Stop()
		}
		resp["Result"] = startFlag
		return resp
	}
	sendRawTransaction := func(cmd map[string]interface{}) map[string]interface{} {
		resp := SendRawTransaction(cmd)
		if userid, ok := resp["Userid"].(string); ok && len(userid) > 0 {
			httpwebsocket.SetTxHashMap(resp["Result"].(string), userid)
			delete(resp, "Userid")
		}
		return resp
	}
	postMethodMap := map[string]Action{
		Api_SendRawTransaction:           {name: "sendrawtransaction", handler: sendRawTransaction},
		Api_SendCustomRecordTxByTransfer: {name: "sendrecord", handler: SendRecorByTransferTransaction},
		Api_OauthServerAddr:              {name: "setoauthserveraddr", handler: SetOauthServerAddr},
		Api_NoticeServerAddr:             {name: "setnoticeserveraddr", handler: SetNoticeServerAddr},
		Api_NoticeServerState:            {name: "setpostblock", handler: SetPushBlockFlag},
		Api_WebsocketState:               {name: "setwebsocketstate", handler: setWebsocketState},
	}
	rt.postMap = postMethodMap
	rt.getMap = getMethodMap
}
func (rt *restServer) getPath(url string) string {

	if strings.Contains(url, strings.TrimRight(Api_Getblockbyheight, ":height")) {
		return Api_Getblockbyheight
	} else if strings.Contains(url, strings.TrimRight(Api_Getblockbyhash, ":hash")) {
		return Api_Getblockbyhash
	} else if strings.Contains(url, strings.TrimRight(Api_Gettransaction, ":hash")) {
		return Api_Gettransaction
	} else if strings.Contains(url, strings.TrimRight(Api_Getasset, ":hash")) {
		if url != Api_GetUnspendOutput {
			return Api_Getasset
		}
	}
	return url
}

func (rt *restServer) initGetHandler() {

	for k, _ := range rt.getMap {
		rt.router.Get(k, func(w http.ResponseWriter, r *http.Request) {

			var reqMsg = make(map[string]interface{})
			var data []byte
			var err error
			var resp map[string]interface{}
			access_token := r.FormValue("access_token")
			auth_type := r.FormValue("auth_type")

			CAkey, errCode, result := rt.checkAccessToken(auth_type, access_token)
			if errCode > 0 && r.URL.Path != Api_OauthServerAddr {
				resp = ResponsePack(errCode)
				resp["Result"] = result
				goto ResponseWrite
			}
			if h, ok := rt.getMap[rt.getPath(r.URL.Path)]; ok {

				reqMsg["Height"] = getParam(r, "height")
				reqMsg["Hash"] = getParam(r, "hash")
				reqMsg["CAkey"] = CAkey
				reqMsg["Raw"] = r.FormValue("raw")
				reqMsg["Addr"] = r.FormValue("addr")
				reqMsg["Assetid"] = r.FormValue("assetid")
				resp = h.handler(reqMsg)
				resp["Action"] = h.name
			} else {
				resp = ResponsePack(Err.INVALID_METHOD)
			}
		ResponseWrite:
			resp["Desc"] = Err.ErrMap[resp["Error"].(int64)]
			data, err = json.Marshal(resp)
			if err != nil {
				log.Fatal("HTTP Handle - json.Marshal: %v", err)
				return
			}
			w.Header().Add("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("content-type", "application/json;charset=utf-8")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write([]byte(data))
		})
	}
}
func (rt *restServer) initPostHandler() {
	for k, _ := range rt.postMap {
		rt.router.Post(k, func(w http.ResponseWriter, r *http.Request) {

			body, _ := ioutil.ReadAll(r.Body)
			defer r.Body.Close()
			var reqMsg = make(map[string]interface{})
			var data []byte
			var err error

			access_token := r.FormValue("access_token")
			auth_type := r.FormValue("auth_type")
			var resp map[string]interface{}
			CAkey, errCode, result := rt.checkAccessToken(auth_type, access_token)
			if errCode > 0 && r.URL.Path != Api_OauthServerAddr {
				resp = ResponsePack(errCode)
				resp["Result"] = result
				goto ResponseWrite
			}

			if h, ok := rt.postMap[rt.getPath(r.URL.Path)]; ok {

				if err = json.Unmarshal(body, &reqMsg); err == nil {
					reqMsg["CAkey"] = CAkey
					reqMsg["Raw"] = r.FormValue("raw")
					reqMsg["Userid"] = r.FormValue("userid")
					resp = h.handler(reqMsg)
					resp["Action"] = h.name

				} else {
					resp = ResponsePack(Err.ILLEGAL_DATAFORMAT)
					resp["Action"] = h.name
					data, _ = json.Marshal(resp)
				}
			}
		ResponseWrite:
			resp["Desc"] = Err.ErrMap[resp["Error"].(int64)]
			data, err = json.Marshal(resp)
			if err != nil {
				log.Fatal("HTTP Handle - json.Marshal: %v", err)
				return
			}
			w.Header().Add("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("content-type", "application/json;charset=utf-8")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write([]byte(data))
		})
	}
	//Options
	for k, _ := range rt.postMap {
		rt.router.Options(k, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("content-type", "application/json;charset=UTF-8")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write([]byte{})
		})
	}

}
func (rt *restServer) Stop() {
	if rt.server != nil {
		rt.server.Shutdown(context.Background())
	}
}
func (rt *restServer) Restart(cmd map[string]interface{}) map[string]interface{} {
	go func() {
		time.Sleep(time.Second)
		rt.Stop()
		time.Sleep(time.Second)
		go rt.Start()
	}()

	var resp = ResponsePack(Err.SUCCESS)
	return resp
}
func (rt *restServer) initTlsListen() (net.Listener, error) {

	CertPath := Parameters.RestCertPath
	KeyPath := Parameters.RestKeyPath

	// load cert
	cert, err := tls.LoadX509KeyPair(CertPath, KeyPath)
	if err != nil {
		log.Error("load keys fail", err)
		return nil, err
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	log.Info("TLS listen port is ", strconv.Itoa(Parameters.HttpRestPort))
	listener, err := tls.Listen("tcp", ":"+strconv.Itoa(Parameters.HttpRestPort), tlsConfig)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	return listener, nil
}
