package main

import (
	"container/list"
	"flag"
	"github.com/garyburd/redigo/redis"
	"time"
	"fmt"
	"strconv"
)


type RedisAccessor struct {
	redis_chan  <-chan interface{}
	rds_pool redis.Pool
}

var redis_server = flag.String("redis_server", "10.171.89.136:9527", "redis server address")
var redis_db_num = flag.Int("redis_db_num", 4, "redis db num")


func newRedisAccessor(ch <-chan interface{}) *RedisAccessor {

	rdsPool := redis.Pool{
		MaxIdle:     10,
		IdleTimeout: time.Second * 300,
		Dial: func() (redis.Conn, error) {
			conn, cErr := redis.Dial("tcp", *redis_server)
			if cErr != nil {
				return nil, cErr
			}

			conn.Do("select", *redis_db_num)
			return conn, nil
		},
	}

	accessor := &RedisAccessor{
		redis_chan:      ch, 
		rds_pool:      rdsPool,
	}
	return accessor
}

func (accessor *RedisAccessor) fetchSystemLingers() *list.List {
	
	net_logger.Printf("enter fetchSystemLingers")

	letters := list.New()
	key := makeLingerKey(uint32(KuaiyuSuperUid))
	vals, err := accessor.rds_pool.Get().Do("lrange", key, 0, -1)
	if err != nil {
		net_logger.Printf("when fetch system linger messages from redis, err(%s)", err.Error())
		return nil
	}

	lists, err := redis.Strings(vals, err)
	if err != nil {
		net_logger.Printf("Error: %s", err.Error())
		return nil
	}

	for i := 0; i < len(lists); i++ {
		letter, err := deserializeLetter([]byte(lists[i]))
		if err == nil {
			if lletter, ok := letter.(*SystemLingerMessage); ok {
				letters.PushBack(lletter)
			}
		} else {
			net_logger.Printf("Error: %s", err.Error())
		}
	}

	net_logger.Printf("[User(1000000) sysletter init]:  has num(%d) system linger letters", letters.Len())

	return letters
}

func (accessor *RedisAccessor) fetchLettersByUid(uid uint32) *list.List {
	
	net_logger.Printf("enter fetchLetterByUid")

	letters := list.New()
	key := makeLetterKey(uid)
	vals, err := accessor.rds_pool.Get().Do("lrange", key, 0, -1)
	if err != nil {
		net_logger.Printf("when fetch offlineletter from redis, uid(%d) err(%s)",
			uid, err.Error())
	}

	lists, err := redis.Strings(vals, err)
	if err != nil {
		net_logger.Printf("Error: %s", err.Error())
		return nil
	}

	for i := 0; i < len(lists); i++ {
		letter, err := deserializeLetter([]byte(lists[i]))
		if err == nil {
			if uletter, ok := letter.(*UserLetterMessage); ok {
				letters.PushBack(uletter)
			} else if sletter, ok := letter.(*SystemLetterMessage); ok {
				letters.PushBack(sletter)
			}

		} else {
			net_logger.Printf("[ User(%d) Deserialize letter]: not parsed letter\n", uid)
		}
	}

	net_logger.Printf("uid(%d) has num(%d) letters", uid, letters.Len())

	return letters
}


func (accessor *RedisAccessor) runRedisAccessor() {
	for item := range accessor.redis_chan {
		if ctx, ok := item.(*StoreLetterCtx); ok {
			accessor.handleStoreLetter(ctx)
		} else if loader, ok := item.(*LoadMessageCtx); ok {
			accessor.handleLoadMessages(loader)
		} else if del, ok := item.(*DeleteLetterCtx); ok {
			accessor.handleDeleteLetter(del)
		} else if delexp, ok := item.(*DelExpiredCtx); ok {
			accessor.handleDeleteExpired(delexp)
		}
	}

}


func (accessor *RedisAccessor) handleStoreLetter(ctx *StoreLetterCtx) {

	net_logger.Printf("enter HandleStoreLetter")

	var key string
	var result string
	var  err error
	if uletter, ok := ctx.letter.(*UserLetterMessage); ok {
		result, err = serializeUserLetter(uletter)
		if err == nil {
			key = makeLetterKey(uletter.To_user.Uid)
		} else {
			return
		}
	} else if sletter, ok := ctx.letter.(*SystemLetterMessage); ok {
		result, err = serializeSystemLetter(sletter)
		if err == nil {
			key = makeLetterKey(sletter.To_user.Uid)
		} else {
			return
		}

	} else if lletter, ok := ctx.letter.(*SystemLingerMessage); ok {
		result, err = serializeSystemLinger(lletter)
		if err == nil {
			key = makeLingerKey(uint32(KuaiyuSuperUid))
		} else {
			net_logger.Printf("[User(redis) store]: Error: %s", err.Error())
			return
		}

	} else {
		return
	}

	//net_logger.Printf("Store: key(%s), result(%s)", key, result)
	n, err := accessor.rds_pool.Get().Do("rpush", key, result)
	if err != nil {
		net_logger.Printf("Error: err(%s)", err.Error())
		return
	}
	net_logger.Printf("[User(redis) store ]: 'rpush' key(%s)'s  letter, total sum(%d)\n", key, n)
}


func (accessor *RedisAccessor) handleLoadMessages(loader *LoadMessageCtx) {

	net_logger.Printf("Enter HandleLoadMessages ")

	wild := fmt.Sprintf("%s*", OFFLINE_LETTER_PREFIX)
	svector, err := redis.Strings(accessor.rds_pool.Get().Do("keys", wild))
	if err != nil {
		net_logger.Printf("Error: (%s)", err.Error())
		return
	} else {
		net_logger.Printf("Redis has prefix(%s), %d letters", OFFLINE_LETTER_PREFIX, len(svector))
	}

	table := make(map[uint32]bool, 100000)
	for _, uidstr := range svector {
		id, err := strconv.ParseUint(string(uidstr[len(OFFLINE_LETTER_PREFIX)+1:]), 10, 32)
		if err != nil {
			net_logger.Printf("Error: uidstr(%s), errmsg(%s)\n", uidstr, err.Error())
			continue
		}

		uid := uint32(id)
		if _, ok := table[uid]; ok {
			continue
		} else {
			table[uid] = true
		}

		letters := accessor.fetchLettersByUid(uid)
		if letters != nil {
			userletter := &UserOfflineLetter {
				uid: uid,
				letter_list : letters,
			}
			loader.uid2letters[uid] = userletter 

			net_logger.Printf("[User(%d) init letter]: has %d letters ", uid, letters.Len())
		} 
	}

	letters := accessor.fetchSystemLingers()
	if letters != nil {
		loader.linger.PushBackList(letters)
		net_logger.Printf("[User(1000000) init linger]: has %d linger letters ", letters.Len())
	} 

	loader.wg.Done()
}

func (accessor *RedisAccessor) handleDeleteLetter(ctx *DeleteLetterCtx) {

	net_logger.Printf("enter HandleDeleteLetter ")
	var key string
	var result string
	var err error
	if uletter, ok := ctx.letter.(*UserLetterMessage); ok {
		result, err = serializeUserLetter(uletter)
		if err == nil {
			key = makeLetterKey(uletter.To_user.Uid)
		} else {
			return
		}
	} else if sletter, ok := ctx.letter.(*SystemLetterMessage); ok {
		result, err = serializeSystemLetter(sletter)
		if err == nil {
			key = makeLetterKey(sletter.To_user.Uid)
		} else {
			return
		}

	} else {
		return
	}

	//net_logger.Printf("Delete: key(%s), result(%s)", key, result)
	vals, err := accessor.rds_pool.Get().Do("lrem", key, 1, result)
	if err != nil {
		net_logger.Printf("redis:'lrem' error , err(%s)", err.Error())
		return
	}

	net_logger.Printf("Delete: 'lrem'  key(%s), count=%d\n", key, vals)

	n, err := redis.Int(vals, err)
	if err != nil {
		net_logger.Printf("Error: %s \n", err.Error())
		return
	}

	net_logger.Printf("HandleDeleteLetter: 'lrem' a letter key(%s), count=%d\n", key, n)

}

func (accessor *RedisAccessor) handleDeleteExpired(ctx *DelExpiredCtx) {

	net_logger.Printf("enter HandleDeleteExpired ")
	if ctx.delcnt <= 0 {
		return
	}

	uid := uint32(KuaiyuSuperUid)
	key := makeLingerKey(uid)
	vals, err := accessor.rds_pool.Get().Do("ltrim", key, ctx.delcnt, -1)
	if err != nil {
		net_logger.Printf("[Uid(%d) redis command]when fetch offlineletter from redis, err(%s)",
			uid, err.Error())
		return
	}

	cnt, err := redis.Int(vals, err)
	if err != nil {
		net_logger.Printf("[Uid(%d) Del expired] error(%s)", uid, err.Error())
		return
	}

	net_logger.Printf("[Uid(%d) DelExpired]: from Redis removed (%d) letters (SHOULD %d)", 
			  uid, cnt, ctx.delcnt)
}

