package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/service/vpclattice"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	anv1alpha1 "github.com/aws/aws-application-networking-k8s/pkg/apis/applicationnetworking/v1alpha1"
	"github.com/aws/aws-application-networking-k8s/pkg/config"
	"github.com/aws/aws-application-networking-k8s/pkg/k8s"
	policy "github.com/aws/aws-application-networking-k8s/pkg/k8s/policyhelper"
	"github.com/aws/aws-application-networking-k8s/pkg/model/core"
	model "github.com/aws/aws-application-networking-k8s/pkg/model/lattice"
	"github.com/aws/aws-application-networking-k8s/pkg/utils/gwlog"
)

type (
	TGP = anv1alpha1.TargetGroupPolicy
)

type InvalidBackendRefError struct {
	BackendRef core.BackendRef
	Reason     string
}

func (e *InvalidBackendRefError) Error() string {
	return e.Reason
}

//go:generate mockgen -destination model_build_targetgroup_mock.go -package gateway github.com/aws/aws-application-networking-k8s/pkg/gateway SvcExportTargetGroupModelBuilder,BackendRefTargetGroupModelBuilder

type SvcExportTargetGroupModelBuilder interface {
	// used during standard model build
	Build(ctx context.Context, svcExport *anv1alpha1.ServiceExport) (core.Stack, error)

	// used for reconciliation of existing target groups against a service export object
	BuildTargetGroup(ctx context.Context, svcExport *anv1alpha1.ServiceExport) (*model.TargetGroup, error)
}

type SvcExportTargetGroupBuilder struct {
	log    gwlog.Logger
	client client.Client
}

func NewSvcExportTargetGroupBuilder(
	log gwlog.Logger,
	client client.Client,
) *SvcExportTargetGroupBuilder {
	return &SvcExportTargetGroupBuilder{
		log:    log,
		client: client,
	}
}

type svcExportTargetGroupModelBuildTask struct {
	log           gwlog.Logger
	client        client.Client
	tgp           *policy.PolicyHandler[*TGP]
	serviceExport *anv1alpha1.ServiceExport
	stack         core.Stack
}

func (b *SvcExportTargetGroupBuilder) Build(
	ctx context.Context,
	svcExport *anv1alpha1.ServiceExport,
) (core.Stack, error) {
	stack := core.NewDefaultStack(core.StackID(k8s.NamespacedName(svcExport)))

	task := &svcExportTargetGroupModelBuildTask{
		log:           b.log,
		serviceExport: svcExport,
		stack:         stack,
		client:        b.client,
		tgp:           policy.NewTargetGroupPolicyHandler(b.log, b.client),
	}

	if err := task.run(ctx); err != nil {
		return nil, err
	}

	return task.stack, nil
}

func (b *SvcExportTargetGroupBuilder) BuildTargetGroup(ctx context.Context, svcExport *anv1alpha1.ServiceExport) (*model.TargetGroup, error) {
	stack := core.NewDefaultStack(core.StackID(k8s.NamespacedName(svcExport)))

	task := &svcExportTargetGroupModelBuildTask{
		log:           b.log,
		serviceExport: svcExport,
		stack:         stack,
		client:        b.client,
		tgp:           policy.NewTargetGroupPolicyHandler(b.log, b.client),
	}

	// If exportedPorts is defined, we need to handle it differently
	// For now, we'll just return the first target group for backward compatibility
	// This is used for reconciliation of existing target groups
	if len(svcExport.Spec.ExportedPorts) > 0 {
		return task.buildTargetGroupForExportedPort(ctx, svcExport.Spec.ExportedPorts[0])
	}

	return task.buildTargetGroup(ctx)
}

func (t *svcExportTargetGroupModelBuildTask) run(ctx context.Context) error {
	// Check if we have exportedPorts defined in the spec
	if len(t.serviceExport.Spec.ExportedPorts) > 0 {
		// Create target groups for each exported port
		for _, exportedPort := range t.serviceExport.Spec.ExportedPorts {
			tg, err := t.buildTargetGroupForExportedPort(ctx, exportedPort)
			if err != nil {
				return fmt.Errorf("failed to build target group for service export %s-%s port %d due to %w",
					t.serviceExport.Name, t.serviceExport.Namespace, exportedPort.Port, err)
			}

			if !tg.IsDeleted {
				err = t.buildTargetsForPort(ctx, tg.ID(), exportedPort.Port)
				if err != nil {
					t.log.Debugf(ctx, "Failed to build targets for service export %s-%s port %d due to %s",
						t.serviceExport.Name, t.serviceExport.Namespace, exportedPort.Port, err)
					return err
				}
			}
		}
		return nil
	}

	// Fall back to legacy behavior if no exportedPorts are defined
	tg, err := t.buildTargetGroup(ctx)
	if err != nil {
		return fmt.Errorf("failed to build target group for service export %s-%s due to %w",
			t.serviceExport.Name, t.serviceExport.Namespace, err)
	}

	if !tg.IsDeleted {
		err = t.buildTargets(ctx, tg.ID())
		if err != nil {
			t.log.Debugf(ctx, "Failed to build targets for service export %s-%s due to %s",
				t.serviceExport.Name, t.serviceExport.Namespace, err)
			return err
		}
	}

	return nil
}

func (t *svcExportTargetGroupModelBuildTask) buildTargets(ctx context.Context, stackTgId string) error {
	targetsBuilder := NewTargetsBuilder(t.log, t.client, t.stack)
	_, err := targetsBuilder.BuildForServiceExport(ctx, t.serviceExport, stackTgId)
	if err != nil {
		return err
	}
	return nil
}

func (t *svcExportTargetGroupModelBuildTask) buildTargetGroupForExportedPort(ctx context.Context, exportedPort anv1alpha1.ExportedPort) (*model.TargetGroup, error) {
	svc := &corev1.Service{}
	noSvcFoundAndDeleting := false
	if err := t.client.Get(ctx, k8s.NamespacedName(t.serviceExport), svc); err != nil {
		if apierrors.IsNotFound(err) && !t.serviceExport.DeletionTimestamp.IsZero() {
			// If we're deleting, it's OK if the service isn't there
			noSvcFoundAndDeleting = true
		} else { // Either it's some other error or we aren't deleting
			return nil, fmt.Errorf("failed to find corresponding k8sService %s, error :%w ",
				k8s.NamespacedName(t.serviceExport), err)
		}
	}

	var ipAddressType string
	var err error
	if noSvcFoundAndDeleting {
		ipAddressType = "IPV4" // just pick a default
	} else {
		ipAddressType, err = buildTargetGroupIpAddressType(svc)
		if err != nil {
			return nil, err
		}
	}

	tgp, err := t.tgp.ObjResolvedPolicy(ctx, t.serviceExport)
	if err != nil {
		return nil, err
	}

	// Get health check config from policy
	_, _, healthCheckConfig, err := parseTargetGroupConfig(tgp)
	if err != nil {
		return nil, err
	}

	// Set protocol and protocolVersion based on routeType
	var protocol, protocolVersion string
	switch exportedPort.RouteType {
	case "HTTP":
		protocol = vpclattice.TargetGroupProtocolHttp
		protocolVersion = vpclattice.TargetGroupProtocolVersionHttp1
	case "GRPC":
		protocol = vpclattice.TargetGroupProtocolHttp
		protocolVersion = vpclattice.TargetGroupProtocolVersionGrpc
	case "TLS":
		protocol = vpclattice.TargetGroupProtocolTcp
		protocolVersion = ""
	default:
		return nil, fmt.Errorf("unsupported route type: %s", exportedPort.RouteType)
	}

	spec := model.TargetGroupSpec{
		Type:              model.TargetGroupTypeIP,
		Port:              exportedPort.Port,
		Protocol:          protocol,
		ProtocolVersion:   protocolVersion,
		IpAddressType:     ipAddressType,
		HealthCheckConfig: healthCheckConfig,
	}
	spec.VpcId = config.VpcID
	spec.K8SSourceType = model.SourceTypeSvcExport
	spec.K8SClusterName = config.ClusterName
	spec.K8SServiceName = t.serviceExport.Name
	spec.K8SServiceNamespace = t.serviceExport.Namespace
	spec.K8SProtocolVersion = protocolVersion

	// Add a tag for the route type to help with identification
	// This is not used by the controller but can be helpful for debugging
	if exportedPort.RouteType != "" {
		spec.K8SProtocolVersion = exportedPort.RouteType
	}

	stackTG, err := model.NewTargetGroup(t.stack, spec)
	if err != nil {
		return nil, err
	}

	stackTG.IsDeleted = !t.serviceExport.DeletionTimestamp.IsZero()
	return stackTG, nil
}

func (t *svcExportTargetGroupModelBuildTask) buildTargetsForPort(ctx context.Context, stackTgId string, port int32) error {
	// This is similar to buildTargets but filters endpoints by the specified port
	targetsBuilder := NewTargetsBuilder(t.log, t.client, t.stack)

	// We need to create a modified ServiceExport with the port annotation set to the specific port
	// This allows us to reuse the existing BuildForServiceExport method
	modifiedServiceExport := t.serviceExport.DeepCopy()
	if modifiedServiceExport.Annotations == nil {
		modifiedServiceExport.Annotations = make(map[string]string)
	}
	modifiedServiceExport.Annotations[portAnnotationsKey] = fmt.Sprintf("%d", port)

	_, err := targetsBuilder.BuildForServiceExport(ctx, modifiedServiceExport, stackTgId)
	if err != nil {
		return err
	}
	return nil
}

func (t *svcExportTargetGroupModelBuildTask) buildTargetGroup(ctx context.Context) (*model.TargetGroup, error) {
	svc := &corev1.Service{}
	noSvcFoundAndDeleting := false
	if err := t.client.Get(ctx, k8s.NamespacedName(t.serviceExport), svc); err != nil {
		if apierrors.IsNotFound(err) && !t.serviceExport.DeletionTimestamp.IsZero() {
			// If we're deleting, it's OK if the service isn't there
			noSvcFoundAndDeleting = true
		} else { // Either it's some other error or we aren't deleting
			return nil, fmt.Errorf("failed to find corresponding k8sService %s, error :%w ",
				k8s.NamespacedName(t.serviceExport), err)
		}
	}

	var ipAddressType string
	var err error
	if noSvcFoundAndDeleting {
		ipAddressType = "IPV4" // Pick a default
	} else {
		ipAddressType, err = buildTargetGroupIpAddressType(svc)
		if err != nil {
			return nil, err
		}
	}

	tgp, err := t.tgp.ObjResolvedPolicy(ctx, t.serviceExport)
	if err != nil {
		return nil, err
	}

	protocol, protocolVersion, healthCheckConfig, err := parseTargetGroupConfig(tgp)
	if err != nil {
		return nil, err
	}

	spec := model.TargetGroupSpec{
		Type:              model.TargetGroupTypeIP,
		Port:              80,
		Protocol:          protocol,
		ProtocolVersion:   protocolVersion,
		IpAddressType:     ipAddressType,
		HealthCheckConfig: healthCheckConfig,
	}
	spec.VpcId = config.VpcID
	spec.K8SSourceType = model.SourceTypeSvcExport
	spec.K8SClusterName = config.ClusterName
	spec.K8SServiceName = t.serviceExport.Name
	spec.K8SServiceNamespace = t.serviceExport.Namespace
	spec.K8SProtocolVersion = protocolVersion

	stackTG, err := model.NewTargetGroup(t.stack, spec)
	if err != nil {
		return nil, err
	}

	stackTG.IsDeleted = !t.serviceExport.DeletionTimestamp.IsZero()
	return stackTG, nil
}

type BackendRefTargetGroupModelBuilder interface {
	Build(ctx context.Context, route core.Route, backendRef core.BackendRef, stack core.Stack) (core.Stack, *model.TargetGroup, error)
}

type BackendRefTargetGroupBuilder struct {
	log    gwlog.Logger
	client client.Client
}

func NewBackendRefTargetGroupBuilder(log gwlog.Logger, client client.Client) BackendRefTargetGroupModelBuilder {
	return &BackendRefTargetGroupBuilder{
		log:    log,
		client: client,
	}
}

type backendRefTargetGroupModelBuildTask struct {
	log        gwlog.Logger
	client     client.Client
	stack      core.Stack
	route      core.Route
	backendRef core.BackendRef
	tgp        *policy.PolicyHandler[*TGP]
}

func (b *BackendRefTargetGroupBuilder) Build(
	ctx context.Context,
	route core.Route,
	backendRef core.BackendRef,
	stack core.Stack,
) (core.Stack, *model.TargetGroup, error) {
	if stack == nil {
		stack = core.NewDefaultStack(core.StackID(k8s.NamespacedName(route.K8sObject())))
		b.log.Debugf(ctx, "Creating new stack for build task")
	}

	task := backendRefTargetGroupModelBuildTask{
		log:        b.log,
		client:     b.client,
		stack:      stack,
		route:      route,
		backendRef: backendRef,
		tgp:        policy.NewTargetGroupPolicyHandler(b.log, b.client),
	}

	stackTg, err := task.buildTargetGroup(ctx)
	if err != nil {
		return nil, nil, err
	}
	return task.stack, stackTg, nil
}

func (t *backendRefTargetGroupModelBuildTask) buildTargetGroup(ctx context.Context) (*model.TargetGroup, error) {
	if string(*t.backendRef.Kind()) == "ServiceImport" {
		return nil, errors.New("not supported for ServiceImport BackendRef")
	}

	tgSpec, err := t.buildTargetGroupSpec(ctx)
	if err != nil {
		return nil, fmt.Errorf("buildTargetGroupSpec err %w", err)
	}

	stackTG, err := model.NewTargetGroup(t.stack, tgSpec)
	if err != nil {
		return nil, err
	}
	t.log.Debugf(ctx, "Added target group for backendRef %s to the stack %s", t.backendRef.Name(), stackTG.ID())

	stackTG.IsDeleted = !t.route.DeletionTimestamp().IsZero() // should always be false
	if !stackTG.IsDeleted {
		t.buildTargets(ctx, stackTG.ID())
	}

	return stackTG, nil
}

func (t *backendRefTargetGroupModelBuildTask) buildTargets(ctx context.Context, stackTgId string) error {
	if string(*t.backendRef.Kind()) == "ServiceImport" {
		t.log.Debugf(ctx, "Service import does not manage targets, returning")
		return nil
	}
	backendRefNsName := getBackendRefNsName(t.route, t.backendRef)
	svc := &corev1.Service{}
	if err := t.client.Get(ctx, backendRefNsName, svc); err != nil {
		return fmt.Errorf("error finding backend service %s due to %s", backendRefNsName, err)
	}

	targetsBuilder := NewTargetsBuilder(t.log, t.client, t.stack)
	_, err := targetsBuilder.Build(ctx, svc, t.backendRef, stackTgId)
	if err != nil {
		return err
	}

	return nil
}

// Now, Only k8sService and serviceImport creation deletion use this function to build TargetGroupSpec, serviceExport does not use this function to create TargetGroupSpec
func (t *backendRefTargetGroupModelBuildTask) buildTargetGroupSpec(ctx context.Context) (model.TargetGroupSpec, error) {
	// note we only build target groups for backendRefs on non-deleted routes
	backendKind := string(*t.backendRef.Kind())
	t.log.Debugf(ctx, "buildTargetGroupSpec, kind %s", backendKind)

	vpc := config.VpcID
	eksCluster := config.ClusterName
	backendRefNsName := getBackendRefNsName(t.route, t.backendRef)
	svc := &corev1.Service{}
	if err := t.client.Get(ctx, backendRefNsName, svc); err != nil {
		if apierrors.IsNotFound(err) {
			return model.TargetGroupSpec{}, &InvalidBackendRefError{
				BackendRef: t.backendRef,
				Reason:     fmt.Sprintf("service %s on route %s not found, backendRef invalid", backendRefNsName.Name, t.route.Name()),
			}
		} else {
			return model.TargetGroupSpec{},
				fmt.Errorf("error finding backend service %s due to %s", backendRefNsName, err)
		}
	}

	var err error
	ipAddressType, err := buildTargetGroupIpAddressType(svc)
	if err != nil {
		return model.TargetGroupSpec{}, err
	}

	tgp, err := t.tgp.ObjResolvedPolicy(ctx, svc)
	if err != nil {
		return model.TargetGroupSpec{}, err
	}

	protocol, protocolVersion, healthCheckConfig, err := parseTargetGroupConfig(tgp)
	if err != nil {
		return model.TargetGroupSpec{}, err
	}

	var parentRefType model.K8SSourceType
	switch t.route.(type) {
	case *core.HTTPRoute:
		parentRefType = model.SourceTypeHTTPRoute
	case *core.GRPCRoute:
		// protocolVersion:GRPC takes precedence over other protocolVersions for k8s svc backendref by GRPCRoutes
		protocolVersion = vpclattice.TargetGroupProtocolVersionGrpc
		parentRefType = model.SourceTypeGRPCRoute
	case *core.TLSRoute:
		// protocol:TCP takes precedence over other protocol for k8s svc backendref by TLSRoutes
		protocol = vpclattice.TargetGroupProtocolTcp
		protocolVersion = ""
		parentRefType = model.SourceTypeTLSRoute
	default:
		return model.TargetGroupSpec{}, fmt.Errorf("unsupported route type %T", t.route)
	}

	spec := model.TargetGroupSpec{
		Type:              model.TargetGroupTypeIP,
		Port:              80,
		Protocol:          protocol,
		ProtocolVersion:   protocolVersion,
		IpAddressType:     ipAddressType,
		HealthCheckConfig: healthCheckConfig,
	}
	spec.VpcId = vpc
	spec.K8SSourceType = parentRefType
	spec.K8SClusterName = eksCluster
	spec.K8SServiceName = backendRefNsName.Name
	spec.K8SServiceNamespace = backendRefNsName.Namespace
	spec.K8SRouteName = t.route.Name()
	spec.K8SRouteNamespace = t.route.Namespace()
	spec.K8SProtocolVersion = protocolVersion

	return spec, nil
}

func getBackendRefNsName(route core.Route, backendRef core.BackendRef) types.NamespacedName {
	var namespace = route.Namespace()
	if backendRef.Namespace() != nil {
		namespace = string(*backendRef.Namespace())
	}

	backendRefNsName := types.NamespacedName{
		Namespace: namespace,
		Name:      string(backendRef.Name()),
	}
	return backendRefNsName
}

func parseTargetGroupConfig(tgp *anv1alpha1.TargetGroupPolicy) (
	protocol string, protocolVersion string, healthCheckConfig *vpclattice.HealthCheckConfig, err error) {
	protocol = "HTTP"
	protocolVersion = vpclattice.TargetGroupProtocolVersionHttp1
	if tgp == nil {
		return protocol, protocolVersion, nil, nil
	}
	if tgp.Spec.Protocol != nil && *tgp.Spec.Protocol == vpclattice.TargetGroupProtocolTcp {
		if tgp.Spec.ProtocolVersion != nil {
			return "", "", nil, fmt.Errorf("protocolVersion is not supported for TCP protocol TargetGroupPolicy")
		}
		protocolVersion = ""
	}
	// Override protocol if specified in the TargetGroupPolicy
	if tgp.Spec.Protocol != nil {
		protocol = *tgp.Spec.Protocol
	}
	// Override protocolVersion if specified in the TargetGroupPolicy for non-TCP protocol
	if tgp.Spec.ProtocolVersion != nil && protocol != vpclattice.TargetGroupProtocolTcp {
		protocolVersion = *tgp.Spec.ProtocolVersion
	}
	healthCheckConfig = parseHealthCheckConfig(tgp)
	return protocol, protocolVersion, healthCheckConfig, nil
}

func parseHealthCheckConfig(tgp *anv1alpha1.TargetGroupPolicy) *vpclattice.HealthCheckConfig {
	hc := tgp.Spec.HealthCheck
	if hc == nil {
		return nil
	}
	var matcher *vpclattice.Matcher
	if hc.StatusMatch != nil {
		matcher = &vpclattice.Matcher{HttpCode: hc.StatusMatch}
	}
	return &vpclattice.HealthCheckConfig{
		Enabled:                    hc.Enabled,
		HealthCheckIntervalSeconds: hc.IntervalSeconds,
		HealthCheckTimeoutSeconds:  hc.TimeoutSeconds,
		HealthyThresholdCount:      hc.HealthyThresholdCount,
		UnhealthyThresholdCount:    hc.UnhealthyThresholdCount,
		Matcher:                    matcher,
		Path:                       hc.Path,
		Port:                       hc.Port,
		Protocol:                   (*string)(hc.Protocol),
		ProtocolVersion:            (*string)(hc.ProtocolVersion),
	}
}

func buildTargetGroupIpAddressType(svc *corev1.Service) (string, error) {
	ipFamilies := svc.Spec.IPFamilies

	if len(ipFamilies) != 1 {
		return "", errors.New("lattice Target Group only supports single stack IP addresses")
	}

	// IpFamilies will always have at least 1 element
	ipFamily := ipFamilies[0]

	switch ipFamily {
	case corev1.IPv4Protocol:
		return vpclattice.IpAddressTypeIpv4, nil
	case corev1.IPv6Protocol:
		return vpclattice.IpAddressTypeIpv6, nil
	default:
		return "", fmt.Errorf("unknown ipFamily: %s", ipFamily)
	}
}

func GetServiceForBackendRef(ctx context.Context, client client.Client, route core.Route, backendRef core.BackendRef) (*corev1.Service, error) {
	svc := &corev1.Service{}
	key := types.NamespacedName{
		Name: string(backendRef.Name()),
	}

	if backendRef.Namespace() != nil {
		key.Namespace = string(*backendRef.Namespace())
	} else {
		key.Namespace = route.Namespace()
	}

	if err := client.Get(ctx, key, svc); err != nil {
		return nil, err
	}

	return svc, nil
}
