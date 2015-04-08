/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apiserver

import (
	"fmt"
	"net/http"
	"net/url"
	gpath "path"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/admission"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/rest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"

	"github.com/emicklei/go-restful"
	"github.com/evanphx/json-patch"
	"github.com/golang/glog"
)

// ContextFunc returns a Context given a request - a context must be returned
type ContextFunc func(req *restful.Request) api.Context

// ScopeNamer handles accessing names from requests and objects
type ScopeNamer interface {
	// Namespace returns the appropriate namespace value from the request (may be empty) or an
	// error.
	Namespace(req *restful.Request) (namespace string, err error)
	// Name returns the name from the request, and an optional namespace value if this is a namespace
	// scoped call. An error is returned if the name is not available.
	Name(req *restful.Request) (namespace, name string, err error)
	// ObjectName returns the namespace and name from an object if they exist, or an error if the object
	// does not support names.
	ObjectName(obj runtime.Object) (namespace, name string, err error)
	// SetSelfLink sets the provided URL onto the object. The method should return nil if the object
	// does not support selfLinks.
	SetSelfLink(obj runtime.Object, url string) error
	// GenerateLink creates a path and query for a given runtime object that represents the canonical path.
	GenerateLink(req *restful.Request, obj runtime.Object) (path, query string, err error)
	// GenerateLink creates a path and query for a list that represents the canonical path.
	GenerateListLink(req *restful.Request) (path, query string, err error)
}

// RequestScope encapsulates common fields across all RESTful handler methods.
type RequestScope struct {
	Namer ScopeNamer
	ContextFunc
	runtime.Codec
	Creater   runtime.ObjectCreater
	Convertor runtime.ObjectConvertor

	Resource   string
	Kind       string
	APIVersion string

	// The version of apiserver resources to use
	ServerAPIVersion string
}

// GetResource returns a function that handles retrieving a single resource from a rest.Storage object.
func GetResource(r rest.Getter, scope RequestScope) restful.RouteFunction {
	return func(req *restful.Request, res *restful.Response) {
		w := res.ResponseWriter
		namespace, name, err := scope.Namer.Name(req)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		ctx := scope.ContextFunc(req)
		ctx = api.WithNamespace(ctx, namespace)

		result, err := r.Get(ctx, name)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		if err := setSelfLink(result, req, scope.Namer); err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		write(http.StatusOK, scope.APIVersion, scope.Codec, result, w, req.Request)
	}
}

// ListResource returns a function that handles retrieving a list of resources from a rest.Storage object.
func ListResource(r rest.Lister, rw rest.Watcher, scope RequestScope, forceWatch bool) restful.RouteFunction {
	return func(req *restful.Request, res *restful.Response) {
		w := res.ResponseWriter

		namespace, err := scope.Namer.Namespace(req)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		ctx := scope.ContextFunc(req)
		ctx = api.WithNamespace(ctx, namespace)

		out, err := queryToObject(req.Request.URL.Query(), scope, "ListOptions")
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		opts := *out.(*api.ListOptions)

		// transform fields
		fn := func(label, value string) (newLabel, newValue string, err error) {
			return scope.Convertor.ConvertFieldLabel(scope.APIVersion, scope.Kind, label, value)
		}
		if opts.FieldSelector, err = opts.FieldSelector.Transform(fn); err != nil {
			// TODO: allow bad request to set field causes based on query parameters
			err = errors.NewBadRequest(err.Error())
			errorJSON(err, scope.Codec, w)
			return
		}

		if (opts.Watch || forceWatch) && rw != nil {
			watcher, err := rw.Watch(ctx, opts.LabelSelector, opts.FieldSelector, opts.ResourceVersion)
			if err != nil {
				errorJSON(err, scope.Codec, w)
				return
			}
			serveWatch(watcher, scope, w, req)
			return
		}

		result, err := r.List(ctx, opts.LabelSelector, opts.FieldSelector)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		if err := setListSelfLink(result, req, scope.Namer); err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		write(http.StatusOK, scope.APIVersion, scope.Codec, result, w, req.Request)
	}
}

// CreateResource returns a function that will handle a resource creation.
func CreateResource(r rest.Creater, scope RequestScope, typer runtime.ObjectTyper, admit admission.Interface) restful.RouteFunction {
	return func(req *restful.Request, res *restful.Response) {
		w := res.ResponseWriter

		// TODO: we either want to remove timeout or document it (if we document, move timeout out of this function and declare it in api_installer)
		timeout := parseTimeout(req.Request.URL.Query().Get("timeout"))

		namespace, err := scope.Namer.Namespace(req)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		ctx := scope.ContextFunc(req)
		ctx = api.WithNamespace(ctx, namespace)

		body, err := readBody(req.Request)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		obj := r.New()
		if err := scope.Codec.DecodeInto(body, obj); err != nil {
			err = transformDecodeError(typer, err, obj, body)
			errorJSON(err, scope.Codec, w)
			return
		}

		err = admit.Admit(admission.NewAttributesRecord(obj, namespace, scope.Resource, "CREATE"))
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		result, err := finishRequest(timeout, func() (runtime.Object, error) {
			out, err := r.Create(ctx, obj)
			if status, ok := out.(*api.Status); ok && err == nil && status.Code == 0 {
				status.Code = http.StatusCreated
			}
			return out, err
		})
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		if err := setSelfLink(result, req, scope.Namer); err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		write(http.StatusCreated, scope.APIVersion, scope.Codec, result, w, req.Request)
	}
}

// PatchResource returns a function that will handle a resource patch
// TODO: Eventually PatchResource should just use AtomicUpdate and this routine should be a bit cleaner
func PatchResource(r rest.Patcher, scope RequestScope, typer runtime.ObjectTyper, admit admission.Interface) restful.RouteFunction {
	return func(req *restful.Request, res *restful.Response) {
		w := res.ResponseWriter

		// TODO: we either want to remove timeout or document it (if we document, move timeout out of this function and declare it in api_installer)
		timeout := parseTimeout(req.Request.URL.Query().Get("timeout"))

		namespace, name, err := scope.Namer.Name(req)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		obj := r.New()
		// PATCH requires same permission as UPDATE
		err = admit.Admit(admission.NewAttributesRecord(obj, namespace, scope.Resource, "UPDATE"))
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		ctx := scope.ContextFunc(req)
		ctx = api.WithNamespace(ctx, namespace)

		original, err := r.Get(ctx, name)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		originalObjJs, err := scope.Codec.Encode(original)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		patchJs, err := readBody(req.Request)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		patchedObjJs, err := jsonpatch.MergePatch(originalObjJs, patchJs)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		if err := scope.Codec.DecodeInto(patchedObjJs, obj); err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		if err := checkName(obj, name, namespace, scope.Namer); err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		result, err := finishRequest(timeout, func() (runtime.Object, error) {
			// update should never create as previous get would fail
			obj, _, err := r.Update(ctx, obj)
			return obj, err
		})
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		if err := setSelfLink(result, req, scope.Namer); err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		write(http.StatusOK, scope.APIVersion, scope.Codec, result, w, req.Request)
	}
}

// UpdateResource returns a function that will handle a resource update
func UpdateResource(r rest.Updater, scope RequestScope, typer runtime.ObjectTyper, admit admission.Interface) restful.RouteFunction {
	return func(req *restful.Request, res *restful.Response) {
		w := res.ResponseWriter

		// TODO: we either want to remove timeout or document it (if we document, move timeout out of this function and declare it in api_installer)
		timeout := parseTimeout(req.Request.URL.Query().Get("timeout"))

		namespace, name, err := scope.Namer.Name(req)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		ctx := scope.ContextFunc(req)
		ctx = api.WithNamespace(ctx, namespace)

		body, err := readBody(req.Request)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		obj := r.New()
		if err := scope.Codec.DecodeInto(body, obj); err != nil {
			err = transformDecodeError(typer, err, obj, body)
			errorJSON(err, scope.Codec, w)
			return
		}

		if err := checkName(obj, name, namespace, scope.Namer); err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		err = admit.Admit(admission.NewAttributesRecord(obj, namespace, scope.Resource, "UPDATE"))
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		wasCreated := false
		result, err := finishRequest(timeout, func() (runtime.Object, error) {
			obj, created, err := r.Update(ctx, obj)
			wasCreated = created
			return obj, err
		})
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		if err := setSelfLink(result, req, scope.Namer); err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		status := http.StatusOK
		if wasCreated {
			status = http.StatusCreated
		}
		writeJSON(status, scope.Codec, result, w)
	}
}

// DeleteResource returns a function that will handle a resource deletion
func DeleteResource(r rest.GracefulDeleter, checkBody bool, scope RequestScope, admit admission.Interface) restful.RouteFunction {
	return func(req *restful.Request, res *restful.Response) {
		w := res.ResponseWriter

		// TODO: we either want to remove timeout or document it (if we document, move timeout out of this function and declare it in api_installer)
		timeout := parseTimeout(req.Request.URL.Query().Get("timeout"))

		namespace, name, err := scope.Namer.Name(req)
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}
		ctx := scope.ContextFunc(req)
		ctx = api.WithNamespace(ctx, namespace)

		options := &api.DeleteOptions{}
		if checkBody {
			body, err := readBody(req.Request)
			if err != nil {
				errorJSON(err, scope.Codec, w)
				return
			}
			if len(body) > 0 {
				if err := scope.Codec.DecodeInto(body, options); err != nil {
					errorJSON(err, scope.Codec, w)
					return
				}
			}
		}

		err = admit.Admit(admission.NewAttributesRecord(nil, namespace, scope.Resource, "DELETE"))
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		result, err := finishRequest(timeout, func() (runtime.Object, error) {
			return r.Delete(ctx, name, options)
		})
		if err != nil {
			errorJSON(err, scope.Codec, w)
			return
		}

		// if the rest.Deleter returns a nil object, fill out a status. Callers may return a valid
		// object with the response.
		if result == nil {
			result = &api.Status{
				Status: api.StatusSuccess,
				Code:   http.StatusOK,
				Details: &api.StatusDetails{
					ID:   name,
					Kind: scope.Kind,
				},
			}
		} else {
			// when a non-status response is returned, set the self link
			if _, ok := result.(*api.Status); !ok {
				if err := setSelfLink(result, req, scope.Namer); err != nil {
					errorJSON(err, scope.Codec, w)
					return
				}
			}
		}
		write(http.StatusOK, scope.APIVersion, scope.Codec, result, w, req.Request)
	}
}

// queryToObject converts query parameters into a structured internal object by
// kind. The caller must cast the returned object to the matching internal Kind
// to use it.
// TODO: add appropriate structured error responses
func queryToObject(query url.Values, scope RequestScope, kind string) (runtime.Object, error) {
	versioned, err := scope.Creater.New(scope.ServerAPIVersion, kind)
	if err != nil {
		// programmer error
		return nil, err
	}
	if err := scope.Convertor.Convert(&query, versioned); err != nil {
		return nil, errors.NewBadRequest(err.Error())
	}
	out, err := scope.Convertor.ConvertToVersion(versioned, "")
	if err != nil {
		// programmer error
		return nil, err
	}
	return out, nil
}

// resultFunc is a function that returns a rest result and can be run in a goroutine
type resultFunc func() (runtime.Object, error)

// finishRequest makes a given resultFunc asynchronous and handles errors returned by the response.
// Any api.Status object returned is considered an "error", which interrupts the normal response flow.
func finishRequest(timeout time.Duration, fn resultFunc) (result runtime.Object, err error) {
	// these channels need to be buffered to prevent the goroutine below from hanging indefinitely
	// when the select statement reads something other than the one the goroutine sends on.
	ch := make(chan runtime.Object, 1)
	errCh := make(chan error, 1)
	go func() {
		if result, err := fn(); err != nil {
			errCh <- err
		} else {
			ch <- result
		}
	}()

	select {
	case result = <-ch:
		if status, ok := result.(*api.Status); ok {
			return nil, errors.FromObject(status)
		}
		return result, nil
	case err = <-errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, errors.NewTimeoutError("request did not complete within allowed duration", 0)
	}
}

// transformDecodeError adds additional information when a decode fails.
func transformDecodeError(typer runtime.ObjectTyper, baseErr error, into runtime.Object, body []byte) error {
	_, kind, err := typer.ObjectVersionAndKind(into)
	if err != nil {
		return err
	}
	if version, dataKind, err := typer.DataVersionAndKind(body); err == nil && len(dataKind) > 0 {
		return errors.NewBadRequest(fmt.Sprintf("%s in version %s cannot be handled as a %s: %v", dataKind, version, kind, baseErr))
	}
	return errors.NewBadRequest(fmt.Sprintf("the object provided is unrecognized (must be of type %s): %v", kind, baseErr))
}

// setSelfLink sets the self link of an object (or the child items in a list) to the base URL of the request
// plus the path and query generated by the provided linkFunc
func setSelfLink(obj runtime.Object, req *restful.Request, namer ScopeNamer) error {
	// TODO: SelfLink generation should return a full URL?
	path, query, err := namer.GenerateLink(req, obj)
	if err != nil {
		return nil
	}

	newURL := *req.Request.URL
	// use only canonical paths
	newURL.Path = gpath.Clean(path)
	newURL.RawQuery = query
	newURL.Fragment = ""

	return namer.SetSelfLink(obj, newURL.String())
}

// checkName checks the provided name against the request
func checkName(obj runtime.Object, name, namespace string, namer ScopeNamer) error {
	if objNamespace, objName, err := namer.ObjectName(obj); err == nil {
		if objName != name {
			return errors.NewBadRequest("the name of the object does not match the name on the URL")
		}
		if len(namespace) > 0 {
			if len(objNamespace) > 0 && objNamespace != namespace {
				return errors.NewBadRequest("the namespace of the object does not match the namespace on the request")
			}
		}
	}
	return nil
}

// setListSelfLink sets the self link of a list to the base URL, then sets the self links
// on all child objects returned.
func setListSelfLink(obj runtime.Object, req *restful.Request, namer ScopeNamer) error {
	if !runtime.IsListType(obj) {
		return nil
	}

	// TODO: List SelfLink generation should return a full URL?
	path, query, err := namer.GenerateListLink(req)
	if err != nil {
		return err
	}
	newURL := *req.Request.URL
	newURL.Path = path
	newURL.RawQuery = query
	// use the path that got us here
	newURL.Fragment = ""
	if err := namer.SetSelfLink(obj, newURL.String()); err != nil {
		glog.V(4).Infof("Unable to set self link on object: %v", err)
	}

	// Set self-link of objects in the list.
	items, err := runtime.ExtractList(obj)
	if err != nil {
		return err
	}
	for i := range items {
		if err := setSelfLink(items[i], req, namer); err != nil {
			return err
		}
	}
	return runtime.SetList(obj, items)

}
