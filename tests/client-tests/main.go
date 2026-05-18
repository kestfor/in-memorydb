package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	lume "github.com/kestfor/in-memorydb/api/lume"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

type Client struct {
	cl        lume.LumeClient
	clientNum int
}

func getConn(n int) *Client {
	client, err := grpc.Dial("127.0.0.1:"+strconv.Itoa(8080+n), grpc.WithInsecure())
	if err != nil {
		panic(err)

	}
	conn := lume.NewLumeClient(client)
	return &Client{cl: conn, clientNum: n}
}

func (c *Client) setKeys(ctx context.Context, n int) error {
	for i := 1; i <= n; i++ {
		k := getKey(c.clientNum, i)
		_, err := c.cl.Set(ctx, &lume.SetRequest{Key: k, CrdtType: lume.Type_TYPE_LWW_REGISTER})
		if err != nil {
			return err
		}
		_, err = c.cl.Apply(ctx, &lume.ApplyRequest{Key: k, Operation: &lume.ApplyRequest_RegisterOperation{RegisterOperation: &lume.ApplyRequest_Register{Value: []byte(strconv.Itoa(i))}}})
		if err != nil {
			return err
		}
	}
	return nil
}

//func (c *Client) deleteKeys(ctx context.Context, n int) error {
//	for i := 1; i <= n; i++ {
//		k := getKey(c.clientNum, i)
//		_, err := c.cl.Delete(ctx, &lume.DeleteRequest{Key: k})
//		if err != nil {
//			return err
//		}
//	}
//	return nil
//}

//func (c *Client) getKeys(ctx context.Context, n int) ([]string, error) {
//	result := make([]string, n)
//	for i := 1; i <= n; i++ {
//		k := getKey(c.clientNum, i)
//		resp, err := c.cl.Get(ctx, &lume.GetRequest{Key: k})
//		if err != nil {
//			return nil, err
//		}
//		result[i-1] = string(resp.GetRegisterData().Val)
//	}
//	return result, nil
//}

func getKey(clNum int, keyN int) string {
	return fmt.Sprintf("client:%d:key:%d", clNum, keyN)
}

func (c *Client) checkAllKeys(ctx context.Context, from, to, keysNum int) (bool, error) {
	wg, ctx := errgroup.WithContext(ctx)
	//start := time.Now()
	//defer func() {
	//	log.Printf("check done in %s", time.Since(start))
	//}()
	for i := from; i <= to; i++ {
		func(i int) {
			wg.Go(func() error {
				wg2, ctx := errgroup.WithContext(ctx)
				sem := make(chan struct{}, 100)
				for kN := 1; kN <= keysNum; kN++ {
					sem <- struct{}{}
					wg2.Go(func() error {
						defer func() { <-sem }()
						k := getKey(i, kN)
						res, err := c.cl.Get(ctx, &lume.GetRequest{Key: k})
						if err != nil {
							return err
						}
						if !res.Ok {
							return fmt.Errorf("key: '%s' is not set", k)
						}
						return nil
					})
				}
				return wg2.Wait()
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

func testSetPerClientsConvergence(clientsNum int, keysPerClient int) {
	wg := sync.WaitGroup{}
	clients := make([]*Client, 0, clientsNum)

	ctx := context.Background()
	for i := 1; i <= clientsNum; i++ {

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

func testClientsPull(clientsNum int, setForClient int, keysPerClient int) {
	wg := sync.WaitGroup{}
	clients := make([]*Client, 0, clientsNum-1)
	ctx := context.Background()

	client := getConn(setForClient)
	log.Printf("setting keys for client %d...", client.clientNum)
	err := client.setKeys(ctx, keysPerClient)
	if err != nil {
		log.Print(err)
		return
	}
	log.Printf("setting keys for client %d done, waiting 10 seconds", client.clientNum)
	time.Sleep(time.Second * 10)
	log.Printf("waiting for consistency...")

	for i := 1; i <= clientsNum; i++ {
		if i == setForClient {
			continue
		}
		wg.Add(1)
		client := getConn(i)
		clients = append(clients, client)

		go func() {
			defer wg.Done()
			ticker := time.NewTicker(time.Second * 1)
			start := time.Now()

			for {
				select {
				case <-ticker.C:
					ok, err := client.checkAllKeys(ctx, setForClient, setForClient, keysPerClient)
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

func main() {
	testClientsPull(5, 1, 10000)
	//testSetPerClientsConvergence(5, 10000)
}
