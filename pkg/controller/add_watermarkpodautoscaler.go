package controller

import (
	"github.com/DataDog/watermarkpodautoscaler/pkg/controller/watermarkpodautoscaler"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, watermarkpodautoscaler.Add)
}
