# Общие параметры кластера.
# https://deckhouse.ru/products/kubernetes-platform/documentation/v1/installing/configuration.html#clusterconfiguration
apiVersion: deckhouse.io/v1
kind: ClusterConfiguration
clusterType: Static
# Адресное пространство подов кластера.
podSubnetCIDR: {{ .PodSubnetCIDR }}
# Адресное пространство сети сервисов кластера.
serviceSubnetCIDR: {{ .ServiceSubnetCIDR }}
kubernetesVersion: "{{ .KubernetesVersion }}" # Можно указать нужную - 1.29/1.31 и пр.
# Домен кластера.
clusterDomain: "{{ .ClusterDomain }}"
---
# Настройки первичной инициализации кластера Deckhouse.
# https://deckhouse.ru/products/kubernetes-platform/documentation/v1/installing/configuration.html#initconfiguration
apiVersion: deckhouse.io/v1
kind: InitConfiguration
deckhouse:
  imagesRepo: {{ .ImagesRepo }}
  registryDockerCfg: {{ .RegistryDockerCfg }}
  devBranch : main
---
# Настройки модуля deckhouse.
# https://deckhouse.ru/products/kubernetes-platform/documentation/v1/modules/deckhouse/configuration.html
apiVersion: deckhouse.io/v1alpha1
kind: ModuleConfig
metadata:
  name: deckhouse
spec:
  version: 1
  enabled: true
  settings:
    bundle: Default
    # Канал обновлений Deckhouse. Канал Early Access достаточно стабилен, его можно использовать в продуктивных окружениях.
    # Если планируется использовать несколько кластеров, то рекомендуется установить на них разные каналы обновлений.
    # Подробнее: https://deckhouse.ru/products/kubernetes-platform/documentation/v1/deckhouse-release-channels.html
    releaseChannel: EarlyAccess
    logLevel: Info
---
# Глобальные настройки Deckhouse.
# https://deckhouse.ru/products/kubernetes-platform/documentation/v1/deckhouse-configure-global.html#%D0%BF%D0%B0%D1%80%D0%B0%D0%BC%D0%B5%D1%82%D1%80%D1%8B
apiVersion: deckhouse.io/v1alpha1
kind: ModuleConfig
metadata:
  name: global
spec:
  version: 2
  settings:
    modules:
      # Шаблон, который будет использоваться для составления адресов системных приложений в кластере.
      # Например, Grafana для %s.example.com будет доступна на домене 'grafana.example.com'.
      # Домен НЕ ДОЛЖЕН совпадать с указанным в параметре clusterDomain ресурса ClusterConfiguration.
      # Можете изменить на свой сразу, либо следовать шагам руководства и сменить его после установки.
      publicDomainTemplate: "{{ .PublicDomainTemplate }}"
---
# Настройки модуля user-authn.
# https://deckhouse.ru/products/kubernetes-platform/documentation/v1/modules/user-authn/configuration.html
apiVersion: deckhouse.io/v1alpha1
kind: ModuleConfig
metadata:
  name: user-authn
spec:
  version: 2
  enabled: true
  settings:
    controlPlaneConfigurator:
      dexCAMode: DoNotNeed
    # Включение доступа к API-серверу Kubernetes через Ingress.
    # https://deckhouse.ru/products/kubernetes-platform/documentation/v1/modules/user-authn/configuration.html#parameters-publishapi
    publishAPI:
      enabled: true
      https:
        mode: Global
        global:
          kubeconfigGeneratorMasterCA: ""
---
# Настройки модуля cni-cilium.
# https://deckhouse.ru/products/kubernetes-platform/documentation/v1/modules/cni-cilium/configuration.html
apiVersion: deckhouse.io/v1alpha1
kind: ModuleConfig
metadata:
  name: cni-cilium
spec:
  version: 1
  # Включить модуль cni-cilium
  enabled: true
  settings:
    # Настройки модуля cni-cilium
    # https://deckhouse.ru/products/kubernetes-platform/documentation/v1/modules/cni-cilium/configuration.html
    tunnelMode: VXLAN
---
# Параметры статического кластера.
# https://deckhouse.ru/products/kubernetes-platform/documentation/v1/installing/configuration.html#staticclusterconfiguration
apiVersion: deckhouse.io/v1
kind: StaticClusterConfiguration
internalNetworkCIDRs:
- {{ .InternalNetworkCIDR }}

