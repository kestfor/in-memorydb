package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github/kestfor/in-memorydb/api/lumepb"
)

var (
	serverAddr string
	timeout    int
	client     pb.LumeClient
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "lume-cli",
		Short: "CLI client for Lume gRPC service",
		Long:  "A command-line interface for interacting with the Lume distributed CRDT store",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			conn, err := grpc.Dial(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return fmt.Errorf("failed to connect: %v", err)
			}
			client = pb.NewLumeClient(conn)
			return nil
		},
	}

	rootCmd.PersistentFlags().StringVarP(&serverAddr, "server", "s", "localhost:9090", "gRPC server address")
	rootCmd.PersistentFlags().IntVarP(&timeout, "timeout", "t", 5, "Request timeout in seconds")

	rootCmd.AddCommand(setCmd())
	rootCmd.AddCommand(getCmd())
	rootCmd.AddCommand(deleteCmd())
	rootCmd.AddCommand(applyCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func setCmd() *cobra.Command {
	var crdtTypeFlag string

	cmd := &cobra.Command{
		Use:   "set <key> [value]",
		Short: "Create a new CRDT object or set its value",
		Long: `Create a new CRDT object with specified key and optional value.
		
Usage modes:
  1. set <key> --type <counter|register>
     Creates an empty CRDT of specified type
     
  2. set <key> <value>
     Auto-detects type from value and applies it:
     - If value is a number: creates counter and increments by that value
     - If value is text: creates register and sets to that value`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			// Mode 1: Only key provided with --type flag
			if len(args) == 1 {
				if crdtTypeFlag == "" {
					return fmt.Errorf("either provide a value or specify --type flag")
				}

				var crdtType pb.Type
				switch crdtTypeFlag {
				case "counter":
					crdtType = pb.Type_TYPE_PN_COUNTER
				case "register":
					crdtType = pb.Type_TYPE_LWW_REGISTER
				default:
					return fmt.Errorf("unknown CRDT type: %s (use 'counter' or 'register')", crdtTypeFlag)
				}

				req := &pb.SetRequest{
					Key:      key,
					CrdtType: crdtType,
				}

				_, err := client.Set(ctx, req)
				if err != nil {
					return fmt.Errorf("failed to set: %v", err)
				}

				fmt.Printf("✓ Successfully created %s with key '%s'\n", crdtTypeFlag, key)
				return nil
			}

			// Mode 2: Key and value provided - auto-detect type and apply
			value := args[1]

			// Try to parse as integer
			if numVal, err := strconv.ParseInt(value, 10, 64); err == nil {
				// It's a number - create counter and increment
				if crdtTypeFlag != "" && crdtTypeFlag != "counter" {
					return fmt.Errorf("value is a number but --type is set to '%s'", crdtTypeFlag)
				}

				// Create counter
				setReq := &pb.SetRequest{
					Key:      key,
					CrdtType: pb.Type_TYPE_PN_COUNTER,
				}
				_, err := client.Set(ctx, setReq)
				if err != nil {
					return fmt.Errorf("failed to create counter: %v", err)
				}

				// Apply increment
				applyReq := &pb.ApplyRequest{
					Key: key,
					Operation: &pb.ApplyRequest_CounterOperationInc{
						CounterOperationInc: &pb.ApplyRequest_CounterInc{
							Val: numVal,
						},
					},
				}
				_, err = client.Apply(ctx, applyReq)
				if err != nil {
					return fmt.Errorf("failed to increment counter: %v", err)
				}

				fmt.Printf("✓ Successfully created counter '%s' and set value to %d\n", key, numVal)
				return nil
			}

			// It's not a number - create register and set value
			if crdtTypeFlag != "" && crdtTypeFlag != "register" {
				return fmt.Errorf("value is text but --type is set to '%s'", crdtTypeFlag)
			}

			// Create register
			setReq := &pb.SetRequest{
				Key:      key,
				CrdtType: pb.Type_TYPE_LWW_REGISTER,
			}
			_, err := client.Set(ctx, setReq)
			if err != nil {
				return fmt.Errorf("failed to create register: %v", err)
			}

			// Apply value
			applyReq := &pb.ApplyRequest{
				Key: key,
				Operation: &pb.ApplyRequest_RegisterOperation{
					RegisterOperation: &pb.ApplyRequest_Register{
						Value: []byte(value),
					},
				},
			}
			_, err = client.Apply(ctx, applyReq)
			if err != nil {
				return fmt.Errorf("failed to set register value: %v", err)
			}

			fmt.Printf("✓ Successfully created register '%s' and set value to '%s'\n", key, value)
			return nil
		},
	}

	cmd.Flags().StringVar(&crdtTypeFlag, "type", "", "CRDT type (counter or register)")

	return cmd
}

func getCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a CRDT object by key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			req := &pb.GetRequest{
				Key: key,
			}

			resp, err := client.Get(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to get: %v", err)
			}

			if !resp.Ok {
				fmt.Printf("✗ Key '%s' not found\n", key)
				return nil
			}

			fmt.Printf("Key: %s\n", key)
			fmt.Printf("Type: %s\n", formatType(resp.CrdtType))

			switch data := resp.Data.(type) {
			case *pb.GetResponse_CounterData:
				fmt.Printf("Value: %d\n", data.CounterData.Val)
			case *pb.GetResponse_RegisterData:
				fmt.Printf("Value: %s\n", string(data.RegisterData.Val))
			default:
				fmt.Println("Value: (unknown)")
			}

			return nil
		},
	}
	return cmd
}

func deleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a CRDT object by key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			req := &pb.DeleteRequest{
				Key: key,
			}

			resp, err := client.Delete(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to delete: %v", err)
			}

			if resp.Ok {
				fmt.Printf("✓ Successfully deleted key '%s'\n", key)
			} else {
				fmt.Printf("✗ Key '%s' not found\n", key)
			}

			return nil
		},
	}
	return cmd
}

func applyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply operations to CRDT objects",
	}

	cmd.AddCommand(applyRegisterCmd())
	cmd.AddCommand(applyIncCmd())
	cmd.AddCommand(applyDecCmd())

	return cmd
}

func applyRegisterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register <key> <value>",
		Short: "Set register value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value := args[1]

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			req := &pb.ApplyRequest{
				Key: key,
				Operation: &pb.ApplyRequest_RegisterOperation{
					RegisterOperation: &pb.ApplyRequest_Register{
						Value: []byte(value),
					},
				},
			}

			_, err := client.Apply(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to apply: %v", err)
			}

			fmt.Printf("✓ Successfully set register '%s' to '%s'\n", key, value)
			return nil
		},
	}
	return cmd
}

func applyIncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inc <key> <value>",
		Short: "Increment counter",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid increment value: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			req := &pb.ApplyRequest{
				Key: key,
				Operation: &pb.ApplyRequest_CounterOperationInc{
					CounterOperationInc: &pb.ApplyRequest_CounterInc{
						Val: value,
					},
				},
			}

			_, err = client.Apply(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to apply: %v", err)
			}

			fmt.Printf("✓ Successfully incremented counter '%s' by %d\n", key, value)
			return nil
		},
	}
	return cmd
}

func applyDecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dec <key> <value>",
		Short: "Decrement counter",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid decrement value: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			req := &pb.ApplyRequest{
				Key: key,
				Operation: &pb.ApplyRequest_CounterOperationDec{
					CounterOperationDec: &pb.ApplyRequest_CounterDec{
						Val: value,
					},
				},
			}

			_, err = client.Apply(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to apply: %v", err)
			}

			fmt.Printf("✓ Successfully decremented counter '%s' by %d\n", key, value)
			return nil
		},
	}
	return cmd
}

func formatType(t pb.Type) string {
	switch t {
	case pb.Type_TYPE_PN_COUNTER:
		return "PN_COUNTER"
	case pb.Type_TYPE_LWW_REGISTER:
		return "LWW_REGISTER"
	default:
		return "NOT_SPECIFIED"
	}
}
