package validatemodels

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/storage/mysql"
	"github.com/openfga/openfga/pkg/storage/postgres"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
	"github.com/openfga/openfga/pkg/typesystem"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	datastoreEngineFlag = "datastore-engine"
	datastoreURIFlag    = "datastore-uri"
)

func NewValidateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate-models",
		Short: "Validate authorization models. NOTE: this command is in beta and may be removed in future releases.",
		Long:  "List all authorization models across all stores and run validations against them.\nNOTE: this command is in beta and may be removed in future releases.",
		RunE:  runValidate,
		Args:  cobra.NoArgs,
	}

	flags := cmd.Flags()
	flags.String(datastoreEngineFlag, "", "the datastore engine")
	flags.String(datastoreURIFlag, "", "the connection uri to the datastore")

	// NOTE: if you add a new flag here, update the function below, too

	cmd.PreRun = bindRunFlagsFunc(flags)

	return cmd
}

type validationResult struct {
	StoreID       string `json:"store_id"`
	ModelID       string `json:"model_id"`
	IsLatestModel bool   `json:"is_latest_model"`
	Error         string `json:"error"`
}

func runValidate(_ *cobra.Command, _ []string) error {
	engine := viper.GetString(datastoreEngineFlag)
	uri := viper.GetString(datastoreURIFlag)

	ctx := context.Background()

	var (
		db  storage.OpenFGADatastore
		err error
	)
	switch engine {
	case "mysql":
		db, err = mysql.New(uri, sqlcommon.NewConfig())
	case "postgres":
		db, err = postgres.New(uri, sqlcommon.NewConfig())
	case "":
		return fmt.Errorf("missing datastore engine type")
	case "memory":
		fallthrough
	default:
		return fmt.Errorf("storage engine '%s' is unsupported", engine)
	}

	if err != nil {
		return fmt.Errorf("failed to open a connection to the datastore: %v", err)
	}

	validationResults, err := ValidateAllAuthorizationModels(ctx, db)
	if err != nil {
		return err
	}

	marshalled, err := json.MarshalIndent(validationResults, " ", "    ")
	if err != nil {
		return fmt.Errorf("error gathering validation results: %w", err)
	}
	fmt.Println(string(marshalled))

	return nil
}

// ValidateAllAuthorizationModels lists all stores and then, for each store, lists all models.
// Then it runs validation on each model.
func ValidateAllAuthorizationModels(ctx context.Context, db storage.OpenFGADatastore) ([]validationResult, error) {
	validationResults := make([]validationResult, 0)

	continuationTokenStores := ""

	for {
		// fetch a page of stores
		stores, tokenStores, err := db.ListStores(ctx, storage.PaginationOptions{
			PageSize: 100,
			From:     continuationTokenStores,
		})
		if err != nil {
			return nil, fmt.Errorf("error reading stores: %w", err)
		}

		// validate each store
		for _, store := range stores {

			latestModelID, err := db.FindLatestAuthorizationModelID(ctx, store.Id)
			if err != nil {
				fmt.Printf("no models in store %s \n", store.Id)
			}

			continuationTokenModels := ""

			for {
				// fetch a page of models for that store
				models, tokenModels, err := db.ReadAuthorizationModels(ctx, store.Id, storage.PaginationOptions{
					PageSize: 100,
					From:     continuationTokenModels,
				})
				if err != nil {
					return nil, fmt.Errorf("error reading authorization models: %w", err)
				}

				// validate each model
				for _, model := range models {
					_, err := typesystem.NewAndValidate(model)

					validationResult := validationResult{
						StoreID:       store.Id,
						ModelID:       model.Id,
						IsLatestModel: model.Id == latestModelID,
					}

					if err != nil {
						validationResult.Error = err.Error()
					}
					validationResults = append(validationResults, validationResult)
				}

				continuationTokenModels = string(tokenModels)

				if continuationTokenModels == "" {
					break
				}
			}
		}

		// next page of stores
		continuationTokenStores = string(tokenStores)

		if continuationTokenStores == "" {
			break
		}
	}

	return validationResults, nil
}