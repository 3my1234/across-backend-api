package realtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Redis time avoids relying on clocks agreeing between future API hosts.
var reserveLease = redis.NewScript(`
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now)
if redis.call('ZCARD',KEYS[1])>=tonumber(ARGV[2]) then return 0 end
redis.call('ZADD',KEYS[1],now+75000,ARGV[1]);redis.call('PEXPIRE',KEYS[1],90000);return 1`)
var renewLease = redis.NewScript(`
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
local expiry=redis.call('ZSCORE',KEYS[1],ARGV[1]);if not expiry or tonumber(expiry)<=now then return 0 end
redis.call('ZADD',KEYS[1],now+75000,ARGV[1]);redis.call('PEXPIRE',KEYS[1],90000);return 1`)

type connectionLease struct {
	redis   *redis.Client
	key, id string
}

func acquireLease(ctx context.Context, client *redis.Client, user string, limit int) (*connectionLease, error) {
	if client == nil || limit < 1 {
		return nil, errors.New("connection lease unavailable")
	}
	if _, err := uuid.Parse(user); err != nil {
		return nil, errors.New("invalid authenticated user")
	}
	digest := sha256.Sum256([]byte(user))
	lease := &connectionLease{redis: client, key: "atl:realtime:connections:" + hex.EncodeToString(digest[:]), id: uuid.NewString()}
	result, err := reserveLease.Run(ctx, client, []string{lease.key}, lease.id, limit).Int()
	if err != nil {
		return nil, err
	}
	if result != 1 {
		return nil, ErrCapacity
	}
	return lease, nil
}
func (l *connectionLease) renew(ctx context.Context) error {
	result, err := renewLease.Run(ctx, l.redis, []string{l.key}, l.id).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return errors.New("connection lease expired")
	}
	return nil
}
func (l *connectionLease) release() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = l.redis.ZRem(ctx, l.key, l.id).Err()
}
