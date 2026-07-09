# Global TODO

## Во время тестов мы будем создавать различные ресурсы с помощью generic функций, нужно продумать очистку (удаление) этих ресурсов по окончании тестов!

## SDK (pkg/e2e)

- [ ] Commander: реализовать `Provider.ConnectTestCluster` (SSH `NodeExecutor` через master) — сейчас заглушка, `e2e.Connect` для commander возвращает `ErrConnectUnsupported`
- [ ] Вернуть capability `DiskManager` (attach/detach доп. блочных устройств): контракт в `pkg/clusterprovider`, DVP-реализация через `VirtualDisk` + `VMBDA`, аксессор `Cluster.Disks()`, conformance `VerifyDiskManager` — отложено с ветки `feat/provider-connect-contract`