import { createRegistry } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { Operations } from "./gen/google/longrunning/operations_pb";
import {
  DiscoveryRunMetadataSchema,
  DiscoveryRunSchema,
  ImportRunMetadataSchema,
  ImportRunSchema,
  WorkspaceService
} from "./gen/lapp/web/v1/web_pb";

const typeRegistry = createRegistry(
  DiscoveryRunMetadataSchema,
  DiscoveryRunSchema,
  ImportRunMetadataSchema,
  ImportRunSchema
);

const transport = createConnectTransport({
  baseUrl: window.location.origin,
  useBinaryFormat: false,
  jsonOptions: {
    registry: typeRegistry
  }
});

export const workspaceClient = createClient(WorkspaceService, transport);
export const operationsClient = createClient(Operations, transport);
