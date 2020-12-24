// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// Code generated by informer-gen. DO NOT EDIT.

package v1alpha1

import (
	"context"
	time "time"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	versioned "github.com/DataDog/watermarkpodautoscaler/pkg/client/clientset/versioned"
	internalinterfaces "github.com/DataDog/watermarkpodautoscaler/pkg/client/informers/externalversions/internalinterfaces"
	v1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/client/listers/datadoghq/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// WatermarkPodAutoscalerInformer provides access to a shared informer and lister for
// WatermarkPodAutoscalers.
type WatermarkPodAutoscalerInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.WatermarkPodAutoscalerLister
}

type watermarkPodAutoscalerInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewWatermarkPodAutoscalerInformer constructs a new informer for WatermarkPodAutoscaler type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewWatermarkPodAutoscalerInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredWatermarkPodAutoscalerInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredWatermarkPodAutoscalerInformer constructs a new informer for WatermarkPodAutoscaler type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredWatermarkPodAutoscalerInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.DatadoghqV1alpha1().WatermarkPodAutoscalers(namespace).List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.DatadoghqV1alpha1().WatermarkPodAutoscalers(namespace).Watch(context.TODO(), options)
			},
		},
		&datadoghqv1alpha1.WatermarkPodAutoscaler{},
		resyncPeriod,
		indexers,
	)
}

func (f *watermarkPodAutoscalerInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredWatermarkPodAutoscalerInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *watermarkPodAutoscalerInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&datadoghqv1alpha1.WatermarkPodAutoscaler{}, f.defaultInformer)
}

func (f *watermarkPodAutoscalerInformer) Lister() v1alpha1.WatermarkPodAutoscalerLister {
	return v1alpha1.NewWatermarkPodAutoscalerLister(f.Informer().GetIndexer())
}
