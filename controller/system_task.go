package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func CreateAbilitiesIndexCleanupSystemTask(c *gin.Context) {
	if !operation_setting.GetAbilitiesIndexCleanupSetting().Enabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "abilities index cleanup is disabled",
		})
		return
	}
	task, created, err := service.EnqueueSystemTask(model.SystemTaskTypeAbilitiesIndexCleanup, service.NewAbilitiesIndexCleanupPayload())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "system_task.abilities_index_cleanup", map[string]interface{}{
		"task_id": task.TaskID,
		"created": created,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}

func CreateLogCleanupSystemTask(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}

	task, err := service.StartLogCleanupTask(targetTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}

func GetCurrentSystemTask(c *gin.Context) {
	taskType := c.Query("type")
	if taskType == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "type is required",
		})
		return
	}

	task, err := model.GetActiveSystemTask(taskType)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}

func ListSystemTasks(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))

	tasks, err := model.ListSystemTasks(limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	responses := make([]model.SystemTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		responses = append(responses, task.ToResponse())
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responses,
	})
}

func GetSystemTask(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "task id is required",
		})
		return
	}

	task, err := model.GetSystemTaskByTaskID(taskID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "task not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}
