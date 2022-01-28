package pkg

import (
	"context"
	"fmt"
	"github.com/pg-sharding/spqr/router/grpcclient"
	routerproto "github.com/pg-sharding/spqr/router/protos"
	"strconv"
	"time"
)

type CoordinatorInterface interface {
	initKeyRanges() (map[Shard][]KeyRange, error)
	isReloadRequired() (bool, error)

	lockKeyRange(rng KeyRange) error
	unlockKeyRange(rng KeyRange) error

	splitKeyRange(border *string) error
	mergeKeyRanges(border *string) error
	moveKeyRange(rng KeyRange, shardTo Shard) error
}

type Coordinator struct {
	maxRetriesCount int
	addr string
	balancerServiceClient routerproto.BalancerServiceClient
	shardServiceClient routerproto.ShardServiceClient
	keyRangeServiceClient routerproto.KeyRangeServiceClient
	operationServiceClient routerproto.OperationServiceClient
}

func (c *Coordinator) Init(addr string, maxRetriesCount int) error {
	c.addr = addr
	c.maxRetriesCount = maxRetriesCount
	connect, err := grpcclient.Dial(addr)
	if err != nil {
		return err
	}
	c.balancerServiceClient = routerproto.NewBalancerServiceClient(connect)

	connect, err = grpcclient.Dial(addr)
	if err != nil {
		return err
	}
	c.shardServiceClient = routerproto.NewShardServiceClient(connect)

	connect, err = grpcclient.Dial(addr)
	if err != nil {
		return err
	}
	c.keyRangeServiceClient = routerproto.NewKeyRangeServiceClient(connect)

	connect, err = grpcclient.Dial(addr)
	if err != nil {
		return err
	}
	c.operationServiceClient = routerproto.NewOperationServiceClient(connect)
	return nil
}

func (c *Coordinator) ShardsList() (*map[int]routerproto.ShardInfo, error) {
	respList, err := c.shardServiceClient.ListShards(context.Background(), &routerproto.ShardRequest{})
	if err != nil {
		return nil, err
	}
	res := map[int]routerproto.ShardInfo{}
	for _, shard := range respList.Shards {
		respShard, err := c.shardServiceClient.GetShardInfo(context.Background(), &routerproto.ShardRequest{
			Id: shard.Id,
		})
		if err != nil {
			return nil, err
		}
		id, err := strconv.Atoi(shard.Id)
		if err != nil {
			return nil, err
		}

		res[id] = routerproto.ShardInfo{ Hosts: respShard.ShardInfo.Hosts, Port: respShard.ShardInfo.Port }
	}

	return &res, nil
}

func (c *Coordinator) waitTilDone(operationID string) error {
	ctx := context.Background()
	retries := 0
	request := &routerproto.GetOperationRequest{
		OperationId: operationID,
	}
	for {
		resp, err := c.operationServiceClient.GetOperation(ctx, request)
		if err == nil {
			if resp.Operation.Status == routerproto.OperationStatus_DONE {
				return nil
			}
			time.Sleep(time.Millisecond * time.Duration(defaultSleepMS))
			continue
		}
		retries++
		fmt.Printf("got error while trying to get operation %s: %s", operationID, err)
		if retries >= c.maxRetriesCount {
			return err
		}
		time.Sleep(time.Millisecond * time.Duration(defaultSleepMS))
	}
}

func (c *Coordinator) initKeyRanges() (map[Shard][]KeyRange, error) {
	resp, err := c.keyRangeServiceClient.ListKeyRange(context.Background(), &routerproto.ListKeyRangeRequest{})
	if err != nil {
		return nil, err
	}

	res := map[Shard][]KeyRange{}
	for _, kr := range resp.KeyRangesInfo {
		id, err := strconv.Atoi(kr.ShardId)
		if err != nil {
			return nil, err
		}
		shard := Shard{id: id}
		_, ok := res[shard]
		if !ok {
			res[shard] = []KeyRange{}
		}
		res[shard] = append(res[shard], KeyRange{left: kr.KeyRange.LowerBound, right: kr.KeyRange.UpperBound})
	}

	return res, nil
}

func (c *Coordinator) isReloadRequired() (bool, error) {
	resp, err := c.balancerServiceClient.ReloadRequired(context.Background(), &routerproto.ReloadRequest{})
	if err != nil {
		return false, err
	}

	return resp.ReloadRequired, nil
}

func (c *Coordinator) lockKeyRange(rng KeyRange) error {
	resp, err := c.keyRangeServiceClient.LockKeyRange(context.Background(), &routerproto.LockKeyRangeRequest{
		KeyRange: &routerproto.KeyRange{LowerBound: rng.left, UpperBound: rng.right},
	})
	if err != nil {
		return err
	}
	return c.waitTilDone(resp.OperationId)
}

func (c Coordinator) unlockKeyRange(rng KeyRange) error {
	resp, err := c.keyRangeServiceClient.UnlockKeyRange(context.Background(), &routerproto.UnlockKeyRangeRequest{
		KeyRange: &routerproto.KeyRange{LowerBound: rng.left, UpperBound: rng.right},
	})
	if err != nil {
		return err
	}
	return c.waitTilDone(resp.OperationId)
}

func (c Coordinator) splitKeyRange(border *string) error {
	resp, err := c.keyRangeServiceClient.SplitKeyRange(context.Background(), &routerproto.SplitKeyRangeRequest{
		Bound: []byte(*border),
	})
	if err != nil {
		return err
	}
	return c.waitTilDone(resp.OperationId)
}

func (c Coordinator) mergeKeyRanges(border *string) error {
	resp, err := c.keyRangeServiceClient.MergeKeyRange(context.Background(), &routerproto.MergeKeyRangeRequest{
		Bound: []byte(*border),
	})
	if err != nil {
		return err
	}
	return c.waitTilDone(resp.OperationId)
}

func (c Coordinator) moveKeyRange(rng KeyRange, shardTo Shard) error {
	resp, err := c.keyRangeServiceClient.MoveKeyRange(context.Background(), &routerproto.MoveKeyRangeRequest{
		KeyRange:  &routerproto.KeyRange{LowerBound: rng.left, UpperBound: rng.right},
		ToShardId: string(int32(shardTo.id)),
	})
	if err != nil {
		return err
	}
	return c.waitTilDone(resp.OperationId)
}
