/*
Copyright 2019 The Knative Authors

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

package domains

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"knative.dev/networking/pkg/apis/networking/v1alpha1"

	"github.com/google/go-cmp/cmp"
	"knative.dev/pkg/apis"
	pkgnet "knative.dev/pkg/network"

	netapi "knative.dev/networking/pkg/apis/networking"
	netcfg "knative.dev/networking/pkg/config"
	"knative.dev/serving/pkg/apis/serving"
	v1 "knative.dev/serving/pkg/apis/serving/v1"
	"knative.dev/serving/pkg/reconciler/route/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Domain: &config.Domain{
			Domains: map[string]*config.LabelSelector{
				"example.com": {},
				"another-example.com": {
					Selector: map[string]string{"app": "prod"},
				},
			},
		},
		Network: &netcfg.Config{
			DefaultIngressClass: "ingress-class-foo",
			DomainTemplate:      netcfg.DefaultDomainTemplate,
		},
	}
}

func TestDomainNameFromTemplate(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name     string
		template string
		args     args
		want     string
		wantErr  bool
		local    bool
	}{{
		name:     "Default",
		template: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
		args:     args{name: "test-name"},
		want:     "test-name.default.example.com",
		local:    false,
	}, {
		name:     "Dash",
		template: "{{.Name}}-{{.Namespace}}.{{.Domain}}",
		args:     args{name: "test-name"},
		want:     "test-name-default.example.com",
		local:    false,
	}, {
		name:     "LocalDash",
		template: "{{.Name}}-{{.Namespace}}.{{.Domain}}",
		args:     args{name: "test-name"},
		want:     pkgnet.GetServiceHostname("test-name", "default"),
		local:    true,
	}, {
		name:     "Short",
		template: "{{.Name}}.{{.Domain}}",
		args:     args{name: "test-name"},
		want:     "test-name.example.com",
		local:    false,
	}, {
		name:     "Too Short", // domain must be at least two segments separated by dots.
		template: "{{.Name}}",
		args:     args{name: "test-name"},
		wantErr:  true,
		local:    false,
	}, {
		name:     "Annotations",
		template: `{{.Name}}.{{ index .Annotations "sub"}}.{{.Domain}}`,
		args:     args{name: "test-name"},
		want:     "test-name.mysub.example.com",
		local:    false,
	}, {
		name:     "Labels",
		template: `{{.Name}}.{{ index .Labels "bus"}}.{{.Domain}}`,
		args:     args{name: "test-name"},
		want:     "test-name.mybus.example.com",
		local:    false,
	}, {
		// This cannot get through our validation, but verify we handle errors.
		name:     "BadVarName",
		template: "{{.Name}}.{{.NNNamespace}}.{{.Domain}}",
		args:     args{name: "test-name"},
		wantErr:  true,
		local:    false,
	}, {
		name:     "Invalid domain name",
		template: "{{.Name}}.{{.Namespace}}.{{.Domain}}.Foo",
		args:     args{name: "test-name"},
		wantErr:  true,
		local:    false,
	}}

	meta := metav1.ObjectMeta{
		SelfLink:  "/apis/serving/v1/namespaces/test/Routes/myapp",
		Name:      "myroute",
		Namespace: "default",
		Labels: map[string]string{
			"route": "myapp",
			"bus":   "mybus",
		},
		Annotations: map[string]string{
			"sub": "mysub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := testConfig()
			cfg.Network.DomainTemplate = tt.template
			ctx = config.ToContext(ctx, cfg)

			if tt.local {
				meta.Labels[netapi.VisibilityLabelKey] = serving.VisibilityClusterLocal
			} else {
				delete(meta.Labels, netapi.VisibilityLabelKey)
			}

			got, err := DomainNameFromTemplate(ctx, meta, tt.args.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("DomainNameFromTemplate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("DomainNameFromTemplate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestURL(t *testing.T) {
	tests := []struct {
		name     string
		scheme   string
		domain   string
		Expected apis.URL
	}{{
		name:   "subdomain",
		scheme: HTTPScheme,
		domain: "current.svc.local.com",
		Expected: apis.URL{
			Scheme: "http",
			Host:   "current.svc.local.com",
		},
	}, {
		name:   "default target",
		scheme: HTTPScheme,
		domain: "example.com",
		Expected: apis.URL{
			Scheme: "http",
			Host:   "example.com",
		},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := *URL(tt.scheme, tt.domain), tt.Expected; !cmp.Equal(want, got) {
				t.Errorf("URL = %v, want: %v", got, want)
			}
		})
	}
}

func TestGetAllDomainsAndTags(t *testing.T) {
	tests := []struct {
		name           string
		domainTemplate string
		tagTemplate    string
		want           map[string]string
		wantErr        bool
	}{{
		name:           "happy case",
		domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
		tagTemplate:    "{{.Name}}-{{.Tag}}",
		want: map[string]string{
			"myroute-target-1.default.example.com": "target-1",
			"myroute-target-2.default.example.com": "target-2",
			"myroute.default.example.com":          "",
		},
	}, {
		name:           "another happy case",
		domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
		tagTemplate:    "{{.Tag}}-{{.Name}}",
		want: map[string]string{
			"target-1-myroute.default.example.com": "target-1",
			"target-2-myroute.default.example.com": "target-2",
			"myroute.default.example.com":          "",
		},
	}, {
		name:           "or appengine style",
		domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
		tagTemplate:    "{{.Tag}}-dot-{{.Name}}",
		want: map[string]string{
			"target-1-dot-myroute.default.example.com": "target-1",
			"target-2-dot-myroute.default.example.com": "target-2",
			"myroute.default.example.com":              "",
		},
	}, {
		name:           "bad template in domain template",
		domainTemplate: "{{.NNName}}.{{.Namespace}}.{{.Domain}}",
		tagTemplate:    "{{.Name}}-{{.Tag}}",
		wantErr:        true,
	}, {
		name:           "bad template in tag template",
		domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
		tagTemplate:    "{{.NNName}}-{{.Tag}}",
		wantErr:        true,
	}, {
		name:           "bad domain name",
		domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
		tagTemplate:    "Foo.{{.Name}}-{{.Tag}}",
		wantErr:        true,
	}}

	route := &v1.Route{
		ObjectMeta: metav1.ObjectMeta{
			SelfLink:  "/apis/serving/v1/namespaces/test/Routes/myapp",
			Name:      "myroute",
			Namespace: "default",
			Labels: map[string]string{
				"route": "myapp",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := testConfig()
			cfg.Network.DomainTemplate = tt.domainTemplate
			cfg.Network.TagTemplate = tt.tagTemplate
			ctx = config.ToContext(ctx, cfg)

			// here, a tag-less major domain will have empty string as the input
			got, err := GetAllDomainsAndTags(ctx, route, []string{"", "target-1", "target-2"}, nil /* visibility */)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAllDomains() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Error("GetAllDomains() diff (-want +got):", diff)
			}
		})
	}
}

func TestGetDomainsForVisibility(t *testing.T) {
	tests := []struct {
		name           string
		tag            string
		visibility     v1alpha1.IngressVisibility
		domainTemplate string
		tagTemplate    string
		want           sets.Set[string]
	}{
		{
			name:           "default template - no tag - cluster-local",
			tag:            "",
			visibility:     v1alpha1.IngressVisibilityClusterLocal,
			domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
			tagTemplate:    "{{.Name}}-{{.Tag}}",
			want: sets.New(
				"myroute.default",
				"myroute.default.svc",
				"myroute.default.svc.cluster.local",
			),
		}, {
			name:           "default template - no tag - external-IP",
			tag:            "",
			visibility:     v1alpha1.IngressVisibilityExternalIP,
			domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
			tagTemplate:    "{{.Name}}-{{.Tag}}",
			want: sets.New(
				"myroute.default.example.com",
			),
		}, {
			name:           "default template - with tag - cluster-local",
			tag:            "test",
			visibility:     v1alpha1.IngressVisibilityClusterLocal,
			domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
			tagTemplate:    "{{.Name}}-{{.Tag}}",
			want: sets.New(
				"myroute-test.default",
				"myroute-test.default.svc",
				"myroute-test.default.svc.cluster.local",
			),
		}, {
			name:           "default template - with tag - external-IP",
			tag:            "test",
			visibility:     v1alpha1.IngressVisibilityExternalIP,
			domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
			tagTemplate:    "{{.Name}}-{{.Tag}}",
			want: sets.New(
				"myroute-test.default.example.com",
			),
		}, {
			name:           "alternative template - no tag - cluster-local",
			tag:            "",
			visibility:     v1alpha1.IngressVisibilityClusterLocal,
			domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
			tagTemplate:    "{{.Tag}}-{{.Name}}",
			want: sets.New(
				"myroute.default",
				"myroute.default.svc",
				"myroute.default.svc.cluster.local",
			),
		}, {
			name:           "alternative template - no tag - external-IP",
			tag:            "",
			visibility:     v1alpha1.IngressVisibilityExternalIP,
			domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
			tagTemplate:    "{{.Tag}}-{{.Name}}",
			want: sets.New(
				"myroute.default.example.com",
			),
		}, {
			name:           "alternative template - with tag - cluster-local",
			tag:            "test",
			visibility:     v1alpha1.IngressVisibilityClusterLocal,
			domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
			tagTemplate:    "{{.Tag}}-{{.Name}}",
			want: sets.New(
				"test-myroute.default",
				"test-myroute.default.svc",
				"test-myroute.default.svc.cluster.local",
			),
		}, {
			name:           "alternative template - with tag - external-IP",
			tag:            "test",
			visibility:     v1alpha1.IngressVisibilityExternalIP,
			domainTemplate: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
			tagTemplate:    "{{.Tag}}-{{.Name}}",
			want: sets.New(
				"test-myroute.default.example.com",
			),
		},
	}

	route := &v1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
			Labels: map[string]string{
				"route": "myapp",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := testConfig()
			cfg.Network.DomainTemplate = tt.domainTemplate
			cfg.Network.TagTemplate = tt.tagTemplate
			ctx = config.ToContext(ctx, cfg)

			got, err := GetDomainsForVisibility(ctx, tt.tag, route, tt.visibility)
			if err != nil {
				t.Errorf("failed calling GetDomainsForVisibility: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Error("GetDomainsForVisibility() diff (-want +got):", diff)
			}
		})
	}
}

func TestIsClusterLocal(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		want   bool
	}{
		{
			name:   "domain is public",
			domain: "k8s.io",
			want:   false,
		},
		{
			name:   "domain is cluster local",
			domain: "my-app.cluster.local",
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsClusterLocal(tt.domain); got != tt.want {
				t.Errorf("IsClusterLocal() = %v, want %v", got, tt.want)
			}
		})
	}
}
