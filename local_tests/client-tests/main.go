package main

import (
	"context"
	"fmt"
	"github.com/kestfor/in-memorydb/api/lumepb"
	"log"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

type Client struct {
	cl        lumepb.LumeClient
	clientNum int
}

func getConn(n int) *Client {
	client, err := grpc.Dial("127.0.0.1:"+strconv.Itoa(50050+n), grpc.WithInsecure())
	if err != nil {
		panic(err)

	}
	conn := lumepb.NewLumeClient(client)
	return &Client{cl: conn, clientNum: n}
}

func (c *Client) setKeys(ctx context.Context, n int) error {
	for i := 1; i <= n; i++ {
		k := getKey(c.clientNum, i)
		_, err := c.cl.Set(ctx, &lumepb.SetRequest{Key: k, CrdtType: lumepb.Type_TYPE_LWW_REGISTER})
		if err != nil {
			return err
		}
		_, err = c.cl.Apply(ctx, &lumepb.ApplyRequest{Key: k, Operation: &lumepb.ApplyRequest_RegisterOperation{RegisterOperation: &lumepb.ApplyRequest_Register{Value: []byte(strconv.Itoa(i))}}})
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) deleteKeys(ctx context.Context, n int) error {
	for i := 1; i <= n; i++ {
		k := getKey(c.clientNum, i)
		_, err := c.cl.Delete(ctx, &lumepb.DeleteRequest{Key: k})
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) getKeys(ctx context.Context, n int) ([]string, error) {
	result := make([]string, n)
	for i := 1; i <= n; i++ {
		k := getKey(c.clientNum, i)
		resp, err := c.cl.Get(ctx, &lumepb.GetRequest{Key: k})
		if err != nil {
			return nil, err
		}
		result[i-1] = string(resp.GetRegisterData().Val)
	}
	return result, nil
}

func getKey(clNum int, keyN int) string {
	return fmt.Sprintf("client:%d:key:%d", clNum, keyN)
}

func (c *Client) checkAllKeys(ctx context.Context, from, to, keysNum int) (bool, error) {
	wg, ctx := errgroup.WithContext(ctx)
	for i := from; i <= to; i++ {
		func(i int) {
			wg.Go(func() error {
				for kN := 1; kN <= keysNum; kN++ {
					k := getKey(i, kN)
					res, err := c.cl.Get(ctx, &lumepb.GetRequest{Key: k})
					if err != nil {
						return err
					}
					if !res.Ok {
						return fmt.Errorf("key: '%s' is not set", k)
					}
				}
				return nil
			})
		}(i)
	}

	err := wg.Wait()
	if err != nil {
		return false, err
	} else {
		return true, nil
	}
}

func main() {
	wg := sync.WaitGroup{}
	clients := make([]*Client, 0, 10)
	keysPerClient := 10000

	ctx := context.Background()
	for i := 1; i <= 10; i++ {

		wg.Add(1)
		client := getConn(i)
		clients = append(clients, client)

		go func() {
			defer wg.Done()
			//err := client.deleteKeys(ctx, keysPerClient)
			//if err != nil {
			//	log.Printf("client:%d delete keys err:%v", i, err)
			//	return
			//}

			log.Printf("setting keys for client %d...", client.clientNum)
			err := client.setKeys(ctx, keysPerClient)
			if err != nil {
				log.Print(err)
				return
			}
			log.Printf("setting keys for client %d done, waiting for consistency...", client.clientNum)

			ticker := time.NewTicker(time.Second * 10)
			start := time.Now()

			for {
				select {
				case <-ticker.C:
					ok, err := client.checkAllKeys(ctx, 1, len(clients), keysPerClient)
					if !ok {
						log.Printf("checkAllKeys failed for client %d: %v, retrying check...", client.clientNum, err)
					} else {
						log.Printf("checkAllKeys passed for client %d, elapsed: %s", client.clientNum, time.Since(start).String())
						return
					}
				}
			}

		}()
	}

	wg.Wait()

}
