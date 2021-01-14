package redisadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/casbin/casbin/v2/util"

	"github.com/casbin/casbin/v2/persist"

	"github.com/casbin/casbin/v2/model"
	"github.com/go-redis/redis/v8"
)

const (
	// The key under which the policies are stored in redis
	PolicyKey = "casbin:policy"
)

// Adapter is an adapter for policy storage based on Redis
type Adapter struct {
	redisCli *redis.Client
}

// NewFromDSN returns a new Adapter by using the given DSN.
// Format: redis://{password}:@{host}:{port}/{database}
// Example: redis://password:@host:6379/0
func NewFromURL(url string) (adapter *Adapter, err error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	redisCli := redis.NewClient(opt)
	if err = redisCli.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping redis: %v", err)
	}

	return NewFromClient(redisCli), nil
}

// NewFromClient returns a new instance of Adapter from an already existing go-redis client.
func NewFromClient(redisCli *redis.Client) (adapter *Adapter) {
	return &Adapter{redisCli: redisCli}
}

// LoadPolicy loads all policy rules from the storage.
func (a *Adapter) LoadPolicy(model model.Model) (err error) {
	ctx := context.Background()

	// Using the LoadPolicyLine handler from the Casbin repo for building rules
	return a.loadPolicy(ctx, model, persist.LoadPolicyLine)
}

func (a *Adapter) loadPolicy(ctx context.Context, model model.Model, handler func(string, model.Model)) (err error) {
	// 0, -1 fetches all entries from the list
	rules, err := a.redisCli.LRange(ctx, PolicyKey, 0, -1).Result()
	if err != nil {
		return err
	}

	// Parse the rules from Redis
	for _, rule := range rules {
		handler(rule, model)
	}

	return
}

// SavePolicy saves all policy rules to the storage.
func (a *Adapter) SavePolicy(model model.Model) (err error) {
	ctx := context.Background()
	var rules []string

	// Serialize the policies into a string slice
	for ptype, assertion := range model["p"] {
		for _, rule := range assertion.Policy {
			rules = append(rules, buildRuleStr(ptype, rule))
		}
	}

	// Append the group policies to the slice
	for ptype, assertion := range model["g"] {
		for _, rule := range assertion.Policy {
			rules = append(rules, buildRuleStr(ptype, rule))
		}
	}

	// If an empty ruleset is saved, the policy is completely deleted from Redis.
	if len(rules) > 0 {
		return a.savePolicy(ctx, rules)
	}
	return a.delPolicy(ctx)
}

func (a *Adapter) savePolicy(ctx context.Context, rules []string) (err error) {
	// Use a transaction for deleting the key & creating a new one.
	// This only uses one round trip to Redis and also makes sure nothing bad happens.
	cmd, err := a.redisCli.TxPipelined(ctx, func(tx redis.Pipeliner) error {
		tx.Del(ctx, PolicyKey)
		tx.RPush(ctx, PolicyKey, strToInterfaceSlice(rules)...)

		return nil
	})
	if err = cmd[0].Err(); err != nil {
		return fmt.Errorf("failed to delete policy key: %v", err)
	}
	if err = cmd[1].Err(); err != nil {
		return fmt.Errorf("failed to save policy: %v", err)
	}

	return
}

func (a *Adapter) delPolicy(ctx context.Context) (err error) {
	if err = a.redisCli.Del(ctx, PolicyKey).Err(); err != nil {
		return err
	}
	return
}

// AddPolicy adds a policy rule to the storage.
func (a *Adapter) AddPolicy(_ string, ptype string, rule []string) (err error) {
	ctx := context.Background()
	return a.addPolicy(ctx, buildRuleStr(ptype, rule))
}

func (a *Adapter) addPolicy(ctx context.Context, rule string) (err error) {
	if err = a.redisCli.RPush(ctx, PolicyKey, rule).Err(); err != nil {
		return err
	}
	return
}

func (a *Adapter) RemovePolicy(_ string, ptype string, rule []string) (err error) {
	ctx := context.Background()

	return a.removePolicy(ctx, buildRuleStr(ptype, rule))
}

func (a *Adapter) removePolicy(ctx context.Context, rule string) (err error) {
	if err = a.redisCli.LRem(ctx, PolicyKey, 1, rule).Err(); err != nil {
		return err
	}
	return
}

func (a *Adapter) RemoveFilteredPolicy(sec string, ptype string, fieldIndex int, fieldValues ...string) error {
	return errors.New("not implemented")
}

// Converts a string slice to an interface{} slice.
// Needed for pushing elements to a redis list.
func strToInterfaceSlice(ss []string) (is []interface{}) {
	for _, s := range ss {
		is = append(is, s)
	}
	return
}

func buildRuleStr(ptype string, rule []string) string {
	return ptype + ", " + util.ArrayToString(rule)
}
