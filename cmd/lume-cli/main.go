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

	pb "in-memorydb/api/lumepb"
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
	cmd := &cobra.Command{
		Use:   "set <key> <type>",
		Short: "Create a new CRDT object",
		Long:  "Create a new CRDT object with specified key and type (pn_counter or lww_register)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			crdtTypeStr := args[1]

			var crdtType pb.Type
			switch crdtTypeStr {
			case "counter":
				crdtType = pb.Type_TYPE_PN_COUNTER
			case "register":
				crdtType = pb.Type_TYPE_LWW_REGISTER
			default:
				return fmt.Errorf("unknown CRDT type: %s (use 'pn_counter' or 'lww_register')", crdtTypeStr)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			req := &pb.SetRequest{
				Key:      key,
				CrdtType: crdtType,
			}

			_, err := client.Set(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to set: %v", err)
			}

			fmt.Printf("✓ Successfully created %s with key '%s'\n", crdtTypeStr, key)
			return nil
		},
	}
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
