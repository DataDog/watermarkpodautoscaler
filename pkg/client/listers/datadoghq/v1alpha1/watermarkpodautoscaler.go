// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	v1alpha1 "github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// WatermarkPodAutoscalerLister helps list WatermarkPodAutoscalers.
type WatermarkPodAutoscalerLister interface {
	// List lists all WatermarkPodAutoscalers in the indexer.
	List(selector labels.Selector) (ret []*v1alpha1.WatermarkPodAutoscaler, err error)
	// WatermarkPodAutoscalers returns an object that can list and get WatermarkPodAutoscalers.
	WatermarkPodAutoscalers(namespace string) WatermarkPodAutoscalerNamespaceLister
	WatermarkPodAutoscalerListerExpansion
}

// watermarkPodAutoscalerLister implements the WatermarkPodAutoscalerLister interface.
type watermarkPodAutoscalerLister struct {
	indexer cache.Indexer
}

// NewWatermarkPodAutoscalerLister returns a new WatermarkPodAutoscalerLister.
func NewWatermarkPodAutoscalerLister(indexer cache.Indexer) WatermarkPodAutoscalerLister {
	return &watermarkPodAutoscalerLister{indexer: indexer}
}

// List lists all WatermarkPodAutoscalers in the indexer.
func (s *watermarkPodAutoscalerLister) List(selector labels.Selector) (ret []*v1alpha1.WatermarkPodAutoscaler, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.WatermarkPodAutoscaler))
	})
	return ret, err
}

// WatermarkPodAutoscalers returns an object that can list and get WatermarkPodAutoscalers.
func (s *watermarkPodAutoscalerLister) WatermarkPodAutoscalers(namespace string) WatermarkPodAutoscalerNamespaceLister {
	return watermarkPodAutoscalerNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// WatermarkPodAutoscalerNamespaceLister helps list and get WatermarkPodAutoscalers.
type WatermarkPodAutoscalerNamespaceLister interface {
	// List lists all WatermarkPodAutoscalers in the indexer for a given namespace.
	List(selector labels.Selector) (ret []*v1alpha1.WatermarkPodAutoscaler, err error)
	// Get retrieves the WatermarkPodAutoscaler from the indexer for a given namespace and name.
	Get(name string) (*v1alpha1.WatermarkPodAutoscaler, error)
	WatermarkPodAutoscalerNamespaceListerExpansion
}

// watermarkPodAutoscalerNamespaceLister implements the WatermarkPodAutoscalerNamespaceLister
// interface.
type watermarkPodAutoscalerNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all WatermarkPodAutoscalers in the indexer for a given namespace.
func (s watermarkPodAutoscalerNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.WatermarkPodAutoscaler, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.WatermarkPodAutoscaler))
	})
	return ret, err
}

// Get retrieves the WatermarkPodAutoscaler from the indexer for a given namespace and name.
func (s watermarkPodAutoscalerNamespaceLister) Get(name string) (*v1alpha1.WatermarkPodAutoscaler, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("watermarkpodautoscaler"), name)
	}
	return obj.(*v1alpha1.WatermarkPodAutoscaler), nil
}
