package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// UpdateTaskBulk 薄入口，实际轮询逻辑在 service 层
func UpdateTaskBulk() {
	service.TaskPollingLoop()
}

func GetAllTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	// 解析其他查询参数
	queryParams := model.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         c.Query("task_id"),
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ChannelID:      c.Query("channel_id"),
	}

	items := model.TaskGetAllTasks(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := model.TaskCountAllTasks(queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(tasksToDto(items, true))
	common.ApiSuccess(c, pageInfo)
}

func GetUserTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	userId := c.GetInt("id")

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)

	queryParams := model.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         c.Query("task_id"),
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
	}

	items := model.TaskGetAllUserTask(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := model.TaskCountAllUserTask(userId, queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(tasksToUserDto(items))
	common.ApiSuccess(c, pageInfo)
}

func tasksToUserDto(tasks []*model.Task) []*dto.UserTaskDto {
	result := make([]*dto.UserTaskDto, len(tasks))
	for i, task := range tasks {
		failReason := ""
		if task.Status == model.TaskStatusFailure {
			failReason = "Task failed"
		}
		resultURL := task.GetResultURL()
		platform := string(task.Platform)
		if task.Platform == constant.TaskPlatformApimartVideo {
			platform = "video"
		}
		if isVideoTaskAction(task.Action) && task.Status == model.TaskStatusSuccess && resultURL != "" {
			resultURL = taskcommon.BuildProxyURL(task.TaskID)
		} else if task.Status != model.TaskStatusSuccess {
			resultURL = ""
		}
		result[i] = &dto.UserTaskDto{
			ID:         task.ID,
			CreatedAt:  task.CreatedAt,
			UpdatedAt:  task.UpdatedAt,
			TaskID:     task.TaskID,
			Platform:   platform,
			Model:      task.Properties.OriginModelName,
			Action:     task.Action,
			Status:     string(task.Status),
			FailReason: failReason,
			ResultURL:  resultURL,
			SubmitTime: task.SubmitTime,
			StartTime:  task.StartTime,
			FinishTime: task.FinishTime,
			Progress:   task.Progress,
		}
	}
	return result
}

func isVideoTaskAction(action string) bool {
	switch action {
	case constant.TaskActionGenerate,
		constant.TaskActionTextGenerate,
		constant.TaskActionFirstTailGenerate,
		constant.TaskActionReferenceGenerate,
		constant.TaskActionRemix:
		return true
	default:
		return false
	}
}

func tasksToDto(tasks []*model.Task, fillUser bool) []*dto.TaskDto {
	var userIdMap map[int]*model.UserBase
	if fillUser {
		userIdMap = make(map[int]*model.UserBase)
		userIds := types.NewSet[int]()
		for _, task := range tasks {
			userIds.Add(task.UserId)
		}
		for _, userId := range userIds.Items() {
			cacheUser, err := model.GetUserCache(userId)
			if err == nil {
				userIdMap[userId] = cacheUser
			}
		}
	}
	result := make([]*dto.TaskDto, len(tasks))
	for i, task := range tasks {
		if fillUser {
			if user, ok := userIdMap[task.UserId]; ok {
				task.Username = user.Username
			}
		}
		result[i] = relay.TaskModel2Dto(task)
	}
	return result
}
