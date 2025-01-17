package handler

import (
	"encoding/json"
	"net/http"
	"proctor/internal/app/service/infra/logger"
	"proctor/internal/app/service/metadata/repository"
	"proctor/internal/pkg/constant"
	modelMetadata "proctor/internal/pkg/model/metadata"
)

type metadataHTTPHandler struct {
	repository repository.MetadataRepository
}

type MetadataHTTPHandler interface {
	Post() http.HandlerFunc
	GetAll() http.HandlerFunc
}

func NewMetadataHTTPHandler(repository repository.MetadataRepository) MetadataHTTPHandler {
	return &metadataHTTPHandler{
		repository: repository,
	}
}

func (handler *metadataHTTPHandler) Post() http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var metadata []modelMetadata.Metadata
		err := json.NewDecoder(request.Body).Decode(&metadata)
		defer request.Body.Close()
		if err != nil {
			logger.Error("Error parsing request body", err.Error())

			response.WriteHeader(http.StatusBadRequest)
			_, _ = response.Write([]byte(constant.ClientError))
			return
		}

		for _, metadata := range metadata {
			err = handler.repository.Save(metadata)
			if err != nil {
				logger.Error("updating metadata to storage, failed", err.Error())

				response.WriteHeader(http.StatusInternalServerError)
				_, _ = response.Write([]byte(constant.ServerError))
				return
			}
		}

		response.WriteHeader(http.StatusCreated)
	}
}

func (handler *metadataHTTPHandler) GetAll() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {

		metadataSlice, err := handler.repository.GetAll()
		if err != nil {
			logger.Error("Error fetching metadata", err.Error())

			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(constant.ServerError))
			return
		}

		metadataByte, err := json.Marshal(metadataSlice)
		if err != nil {
			logger.Error("Error marshalling jobs metadata in json", err.Error())

			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(constant.ServerError))
			return
		}

		_, _ = w.Write(metadataByte)
	}
}
