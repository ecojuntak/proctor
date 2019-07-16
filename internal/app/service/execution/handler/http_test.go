package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"proctor/internal/app/service/execution/handler/parameter"
	handlerStatus "proctor/internal/app/service/execution/handler/status"
	"proctor/internal/app/service/execution/model"
	"proctor/internal/app/service/execution/repository"
	"proctor/internal/app/service/execution/service"
	"proctor/internal/app/service/execution/status"
	"proctor/internal/app/service/infra/kubernetes"
	"proctor/internal/pkg/constant"
	"proctor/internal/pkg/utility"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jmoiron/sqlx/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"github.com/urfave/negroni"
)

type ExecutionHttpHandlerTestSuite struct {
	suite.Suite
	mockExecutionerService           *service.MockExecutionService
	mockExecutionerContextRepository *repository.MockExecutionContextRepository
	mockKubernetesClient             kubernetes.MockKubernetesClient
	testExecutionHttpHandler         ExecutionHttpHandler

	Client     *http.Client
	TestServer *httptest.Server
}

func (suite *ExecutionHttpHandlerTestSuite) SetupTest() {
	suite.mockExecutionerService = &service.MockExecutionService{}
	suite.mockExecutionerContextRepository = &repository.MockExecutionContextRepository{}
	suite.mockKubernetesClient = kubernetes.MockKubernetesClient{}
	suite.testExecutionHttpHandler = NewExecutionHttpHandler(suite.mockExecutionerService, suite.mockExecutionerContextRepository)

	suite.Client = &http.Client{}
	router := mux.NewRouter()
	router.HandleFunc("/jobs/execute/{name}/status", suite.testExecutionHttpHandler.Status()).Methods("GET")
	n := negroni.Classic()
	n.UseHandler(router)
	suite.TestServer = httptest.NewServer(n)
}

type logsHandlerServer struct {
	*httptest.Server
}

var logsHandlerDialer = websocket.Dialer{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

const (
	logsHandlerPath       = "/jobs/logs"
	logsHandlerRawQuery   = "job_name=1"
	logsHandlerRequestURI = logsHandlerPath
)

func (suite *ExecutionHttpHandlerTestSuite) newServer() *logsHandlerServer {
	var s logsHandlerServer
	s.Server = httptest.NewServer(suite.testExecutionHttpHandler.Logs())
	s.Server.URL += logsHandlerRequestURI
	s.URL = makeWsProto(s.Server.URL)
	return &s
}

func makeWsProto(s string) string {
	return "ws" + strings.TrimPrefix(s, "http")
}

func (suite *ExecutionHttpHandlerTestSuite) TestSuccessfulJobExecutionLogsWhenFinishedHttpHandler() {
	t := suite.T()

	s := suite.newServer()
	defer s.Close()

	executionContextId := uint64(1)
	userEmail := "mrproctor@example.com"
	job := parameter.Job{
		Name: "sample-job-name",
		Args: map[string]string{"argOne": "sample-arg"},
	}
	context := &model.ExecutionContext{
		ExecutionID: executionContextId,
		UserEmail:   userEmail,
		JobName:     job.Name,
		ImageTag:    "test",
		Args:        job.Args,
		CreatedAt:   time.Now(),
		Status:      status.Finished,
		Output:      types.GzippedText("test"),
	}

	buffer := utility.NewBuffer()
	buffer.Write([]byte("test\n"))

	suite.mockExecutionerContextRepository.On("GetById", executionContextId).Return(context, nil).Once()
	defer suite.mockExecutionerContextRepository.AssertExpectations(t)

	c, _, err := websocket.DefaultDialer.Dial(s.URL+"?"+logsHandlerRawQuery, nil)
	assert.NoError(t, err)
	defer c.Close()

	_, firstMessage, err := c.ReadMessage()
	assert.NoError(t, err)
	assert.Equal(t, "test", string(firstMessage))
}

func (suite *ExecutionHttpHandlerTestSuite) TestSuccessfulJobExecutionLogsWhenReadyHttpHandler() {
	t := suite.T()

	s := suite.newServer()
	defer s.Close()

	executionContextId := uint64(1)
	userEmail := "mrproctor@example.com"
	job := parameter.Job{
		Name: "sample-job-name",
		Args: map[string]string{"argOne": "sample-arg"},
	}
	context := &model.ExecutionContext{
		ExecutionID: executionContextId,
		UserEmail:   userEmail,
		Name:        "1",
		JobName:     job.Name,
		ImageTag:    "test",
		Args:        job.Args,
		CreatedAt:   time.Now(),
		Status:      status.PodReady,
		Output:      types.GzippedText("test"),
	}

	buffer := utility.NewBuffer()
	buffer.Write([]byte("test1\ntest2\ntest3\n"))

	suite.mockExecutionerService.On("StreamJobLogs", "1", time.Duration(30)*time.Second).Return(buffer, nil).Once()
	defer suite.mockExecutionerService.AssertExpectations(t)
	suite.mockExecutionerContextRepository.On("GetById", executionContextId).Return(context, nil).Once()
	defer suite.mockExecutionerContextRepository.AssertExpectations(t)

	c, _, err := websocket.DefaultDialer.Dial(s.URL+"?"+logsHandlerRawQuery, nil)
	assert.NoError(t, err)
	defer c.Close()

	_, firstMessage, err := c.ReadMessage()
	assert.NoError(t, err)
	assert.Equal(t, "test1", string(firstMessage))

	_, secondMessage, err := c.ReadMessage()
	assert.NoError(t, err)
	assert.Equal(t, "test2", string(secondMessage))

	_, thirdMessage, err := c.ReadMessage()
	assert.NoError(t, err)
	assert.Equal(t, "test3", string(thirdMessage))
}

func (suite *ExecutionHttpHandlerTestSuite) TestSuccessfulJobExecutionStatusHttpHandler() {
	t := suite.T()

	executionContextId := uint64(1)
	userEmail := "mrproctor@example.com"
	job := parameter.Job{
		Name: "sample-job-name",
		Args: map[string]string{"argOne": "sample-arg"},
	}
	context := &model.ExecutionContext{
		ExecutionID: executionContextId,
		UserEmail:   userEmail,
		JobName:     job.Name,
		ImageTag:    "test",
		Args:        job.Args,
		CreatedAt:   time.Now(),
		Status:      status.Finished,
	}
	responseMap := map[string]string{
		"ExecutionId": fmt.Sprint(executionContextId),
		"JobName":     context.JobName,
		"ImageTag":    context.ImageTag,
		"CreatedAt":   context.CreatedAt.String(),
		"Status":      string(context.Status),
	}

	responseBody, err := json.Marshal(responseMap)
	assert.NoError(t, err)

	req := httptest.NewRequest("GET", fmt.Sprintf("/execute/%s/status", fmt.Sprint(executionContextId)), bytes.NewReader([]byte("")))
	req = mux.SetURLVars(req, map[string]string{"name": fmt.Sprint(executionContextId)})
	responseRecorder := httptest.NewRecorder()

	suite.mockExecutionerContextRepository.On("GetById", executionContextId).Return(context, nil).Once()
	defer suite.mockExecutionerContextRepository.AssertExpectations(t)

	suite.testExecutionHttpHandler.Status()(responseRecorder, req)

	assert.Equal(t, http.StatusOK, responseRecorder.Code)
	assert.Equal(t, string(responseBody), responseRecorder.Body.String())
}

func (suite *ExecutionHttpHandlerTestSuite) TestMalformedRequestforJobExecutionStatusHttpHandler() {
	t := suite.T()

	executionContextId := uint64(1)

	req := httptest.NewRequest("GET", fmt.Sprintf("/execute/%s/status", fmt.Sprint(executionContextId)), bytes.NewReader([]byte("test")))
	req = mux.SetURLVars(req, map[string]string{"name": "notfound"})
	responseRecorder := httptest.NewRecorder()

	suite.testExecutionHttpHandler.Status()(responseRecorder, req)

	assert.Equal(t, http.StatusBadRequest, responseRecorder.Code)
	assert.Equal(t, string(handlerStatus.PathParameterError), responseRecorder.Body.String())
}

func (suite *ExecutionHttpHandlerTestSuite) TestSuccessfulJobExecutionPostHttpHandler() {
	t := suite.T()

	userEmail := "mrproctor@example.com"
	job := parameter.Job{
		Name: "sample-job-name",
		Args: map[string]string{"argOne": "sample-arg"},
	}
	context := &model.ExecutionContext{
		UserEmail: userEmail,
		JobName:   job.Name,
		Args:      job.Args,
		Status:    status.Finished,
	}
	responseMap := map[string]string{
		"CreatedAt":     context.CreatedAt.String(),
		"ExecutionId":   "0",
		"ExecutionName": "test",
		"ImageTag":      "",
		"JobName":       context.JobName,
		"Status":        string(context.Status),
	}

	requestBody, err := json.Marshal(job)
	assert.NoError(t, err)

	responseBody, err := json.Marshal(responseMap)
	assert.NoError(t, err)

	req := httptest.NewRequest("POST", "/execute", bytes.NewReader(requestBody))
	req.Header.Set(constant.UserEmailHeaderKey, userEmail)
	responseRecorder := httptest.NewRecorder()

	suite.mockExecutionerService.On("Execute", job.Name, userEmail, job.Args).Return(context, "test", nil).Once()
	defer suite.mockExecutionerService.AssertExpectations(t)

	suite.testExecutionHttpHandler.Post()(responseRecorder, req)

	assert.Equal(t, http.StatusCreated, responseRecorder.Code)
	assert.Equal(t, string(responseBody), responseRecorder.Body.String())
}

func (suite *ExecutionHttpHandlerTestSuite) TestMalformedRequestJobExecutionPostHttpHandler() {
	t := suite.T()

	req := httptest.NewRequest("POST", "/execute", bytes.NewReader([]byte("test")))
	responseRecorder := httptest.NewRecorder()

	suite.testExecutionHttpHandler.Post()(responseRecorder, req)

	assert.Equal(t, http.StatusBadRequest, responseRecorder.Code)
	assert.Equal(t, string(handlerStatus.MalformedRequest), responseRecorder.Body.String())
}

func (suite *ExecutionHttpHandlerTestSuite) TestGenericErrorJobExecutionPostHttpHandler() {
	t := suite.T()

	userEmail := "mrproctor@example.com"
	job := parameter.Job{
		Name: "sample-job-name",
		Args: map[string]string{"argOne": "sample-arg"},
	}
	context := &model.ExecutionContext{
		UserEmail: userEmail,
		JobName:   job.Name,
		Args:      job.Args,
		Status:    status.Finished,
	}
	genericError := errors.New("Something went wrong")

	requestBody, err := json.Marshal(job)
	assert.NoError(t, err)

	req := httptest.NewRequest("POST", "/execute", bytes.NewReader(requestBody))
	req.Header.Set(constant.UserEmailHeaderKey, userEmail)
	responseRecorder := httptest.NewRecorder()

	suite.mockExecutionerService.On("Execute", job.Name, userEmail, job.Args).Return(context, "test", genericError).Once()
	defer suite.mockExecutionerService.AssertExpectations(t)

	suite.testExecutionHttpHandler.Post()(responseRecorder, req)

	assert.Equal(t, http.StatusInternalServerError, responseRecorder.Code)
	assert.Equal(t, fmt.Sprintf("%s , Errors Detail %s", handlerStatus.JobExecutionError, genericError), responseRecorder.Body.String())
}

func TestExecutionHttpHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(ExecutionHttpHandlerTestSuite))
}
