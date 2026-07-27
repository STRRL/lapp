import { ConnectError } from "@connectrpc/connect";
import { anyUnpack, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { create } from "zustand";
import { operationsClient, workspaceClient } from "./api";
import {
  DiscoveryRun,
  DiscoveryRunMetadataSchema,
  DiscoveryRunState,
  ImportRun,
  ImportRunMetadataSchema,
  ImportRunState,
  LogFile,
  Pattern,
  RecentImportQuery,
  UnmatchedErrorLine,
  Workspace
} from "./gen/lapp/web/v1/web_pb";

export type Tab = "logs" | "imports" | "patterns" | "errors";

export type ImportInput = {
  project: string;
  filter: string;
  from: Date;
  to: Date;
  limit: number;
};

type AppState = {
  workspaces: Workspace[];
  selectedWorkspaceName: string;
  logFiles: LogFile[];
  importRuns: ImportRun[];
  recentImportQueries: RecentImportQuery[];
  discoveryRuns: DiscoveryRun[];
  activeOperationName: string;
  selectedRunName: string;
  selectedPatternName: string;
  patterns: Pattern[];
  errorPatterns: Pattern[];
  unmatchedErrors: UnmatchedErrorLine[];
  activeTab: Tab;
  newWorkspaceID: string;
  notice: string;
  busy: boolean;
};

type AppActions = {
  setActiveTab: (tab: Tab) => void;
  setNewWorkspaceID: (value: string) => void;
  selectWorkspace: (name: string) => void;
  selectRun: (name: string) => void;
  selectPattern: (name: string) => void;
  runAction: (action: () => Promise<void>) => Promise<void>;
  refreshWorkspaces: () => Promise<void>;
  refreshWorkspaceDetails: (workspaceName?: string) => Promise<void>;
  refreshRecentImportQueries: () => Promise<void>;
  refreshActiveOperation: () => Promise<void>;
  refreshResults: (runName?: string) => Promise<void>;
  createWorkspace: () => Promise<void>;
  deleteWorkspace: (workspace: Workspace) => Promise<void>;
  uploadFiles: (files: FileList | null) => Promise<void>;
  deleteLogFile: (logFile: LogFile) => Promise<void>;
  startImport: (input: ImportInput) => Promise<void>;
  startDiscovery: () => Promise<void>;
};

const emptyResults = {
  patterns: [],
  errorPatterns: [],
  unmatchedErrors: []
};

export const useAppStore = create<AppState & AppActions>()((set, get) => ({
  workspaces: [],
  selectedWorkspaceName: "",
  logFiles: [],
  importRuns: [],
  recentImportQueries: [],
  discoveryRuns: [],
  activeOperationName: "",
  selectedRunName: "",
  selectedPatternName: "",
  patterns: [],
  errorPatterns: [],
  unmatchedErrors: [],
  activeTab: "logs",
  newWorkspaceID: "",
  notice: "",
  busy: false,

  setActiveTab: (activeTab) => set({ activeTab }),
  setNewWorkspaceID: (newWorkspaceID) => set({ newWorkspaceID }),
  selectWorkspace: (selectedWorkspaceName) =>
    set({
      selectedWorkspaceName,
      selectedRunName: "",
      selectedPatternName: "",
      logFiles: [],
      importRuns: [],
      discoveryRuns: [],
      activeOperationName: "",
      ...emptyResults
    }),
  selectRun: (selectedRunName) => set({ selectedRunName, selectedPatternName: "" }),
  selectPattern: (selectedPatternName) => set({ selectedPatternName }),

  runAction: async (action) => {
    set({ busy: true, notice: "" });
    try {
      await action();
    } catch (error) {
      set({ notice: errorMessage(error) });
    } finally {
      set({ busy: false });
    }
  },

  refreshWorkspaces: async () => {
    const response = await workspaceClient.listWorkspaces({});
    const selected = get().selectedWorkspaceName;
    set({
      workspaces: response.workspaces,
      selectedWorkspaceName: selected || response.workspaces[0]?.name || ""
    });
  },

  refreshWorkspaceDetails: async (workspaceName = get().selectedWorkspaceName) => {
    if (!workspaceName) {
      set({
        logFiles: [],
        importRuns: [],
        discoveryRuns: [],
        activeOperationName: "",
        selectedRunName: "",
        ...emptyResults
      });
      return;
    }

    const [filesResponse, importRunsResponse, discoveryRunsResponse] = await Promise.all([
      workspaceClient.listLogFiles({ parent: workspaceName }),
      workspaceClient.listImportRuns({ parent: workspaceName }),
      workspaceClient.listDiscoveryRuns({ parent: workspaceName })
    ]);
    const currentRun = get().selectedRunName;
    const latestSuccessful =
      discoveryRunsResponse.discoveryRuns.find((run) => run.state === DiscoveryRunState.SUCCEEDED)?.name || "";
    const selectedRunName =
      currentRun && discoveryRunsResponse.discoveryRuns.some((run) => run.name === currentRun)
        ? currentRun
        : latestSuccessful;
    const activeOperationName =
      findActiveOperationName(importRunsResponse.importRuns, discoveryRunsResponse.discoveryRuns) || "";

    set({
      logFiles: filesResponse.logFiles,
      importRuns: importRunsResponse.importRuns,
      discoveryRuns: discoveryRunsResponse.discoveryRuns,
      activeOperationName,
      selectedRunName
    });
  },

  refreshRecentImportQueries: async () => {
    const response = await workspaceClient.listRecentImportQueries({});
    set({ recentImportQueries: response.recentQueries });
  },

  refreshActiveOperation: async () => {
    const activeOperationName = get().activeOperationName;
    if (!activeOperationName) return;

    const operation = await operationsClient.getOperation({ name: activeOperationName });
    const importMetadata = operation.metadata
      ? anyUnpack(operation.metadata, ImportRunMetadataSchema)
      : undefined;
    const discoveryMetadata = operation.metadata
      ? anyUnpack(operation.metadata, DiscoveryRunMetadataSchema)
      : undefined;

    set((state) => ({
      importRuns: importMetadata?.importRun
        ? replaceRun(state.importRuns, importMetadata.importRun)
        : state.importRuns,
      discoveryRuns: discoveryMetadata?.discoveryRun
        ? replaceRun(state.discoveryRuns, discoveryMetadata.discoveryRun)
        : state.discoveryRuns,
      selectedRunName: discoveryMetadata?.discoveryRun?.name || state.selectedRunName,
      activeOperationName: operation.done ? "" : activeOperationName
    }));

    if (operation.done) {
      await get().refreshWorkspaceDetails();
      await get().refreshWorkspaces();
      await get().refreshRecentImportQueries();
      await get().refreshResults();
    }
  },

  refreshResults: async (runName = get().selectedRunName) => {
    if (!runName) {
      set(emptyResults);
      return;
    }

    const [patternsResponse, errorsResponse] = await Promise.all([
      workspaceClient.listPatterns({ parent: runName }),
      workspaceClient.getErrorsView({ parent: runName })
    ]);
    set({
      patterns: patternsResponse.patterns,
      errorPatterns: errorsResponse.errorPatterns.map((entry) => entry.pattern).filter(isPattern),
      unmatchedErrors: errorsResponse.unmatchedErrorLines,
      selectedPatternName:
        get().selectedPatternName && patternsResponse.patterns.some((pattern) => pattern.name === get().selectedPatternName)
          ? get().selectedPatternName
          : ""
    });
  },

  createWorkspace: async () => {
    const workspaceId = get().newWorkspaceID.trim();
    if (!workspaceId) return;

    const response = await workspaceClient.createWorkspace({ workspaceId });
    const selectedWorkspaceName = response.workspace?.name || "";
    set({
      newWorkspaceID: "",
      selectedWorkspaceName,
      selectedRunName: "",
      selectedPatternName: "",
      importRuns: [],
      activeOperationName: "",
      ...emptyResults
    });
    await get().refreshWorkspaces();
    await get().refreshWorkspaceDetails(selectedWorkspaceName);
  },

  deleteWorkspace: async (workspace) => {
    if (!window.confirm(`Permanently delete ${workspace.workspaceId}?`)) return;

    await workspaceClient.deleteWorkspace({ name: workspace.name });
    set({
      selectedWorkspaceName: "",
      selectedRunName: "",
      selectedPatternName: "",
      logFiles: [],
      importRuns: [],
      discoveryRuns: [],
      activeOperationName: "",
      ...emptyResults
    });
    await get().refreshWorkspaces();
  },

  uploadFiles: async (files) => {
    const selectedWorkspaceName = get().selectedWorkspaceName;
    if (!files || !selectedWorkspaceName) return;

    for (const file of Array.from(files)) {
      await workspaceClient.uploadLogFile({
        parent: selectedWorkspaceName,
        fileName: file.name,
        content: new Uint8Array(await file.arrayBuffer())
      });
    }

    await get().refreshWorkspaceDetails(selectedWorkspaceName);
    await get().refreshWorkspaces();
  },

  deleteLogFile: async (logFile) => {
    if (!window.confirm(`Delete ${logFile.fileName}?`)) return;

    await workspaceClient.deleteLogFile({ name: logFile.name });
    await get().refreshWorkspaceDetails();
    await get().refreshWorkspaces();
  },

  startImport: async (input) => {
    const selectedWorkspaceName = get().selectedWorkspaceName;
    if (!selectedWorkspaceName) return;

    const operation = await workspaceClient.createImportRun({
      parent: selectedWorkspaceName,
      project: input.project,
      filter: input.filter,
      from: timestampFromDate(input.from),
      to: timestampFromDate(input.to),
      limit: input.limit
    });
    const metadata = operation.metadata
      ? anyUnpack(operation.metadata, ImportRunMetadataSchema)
      : undefined;
    set((state) => ({
      activeTab: "imports",
      activeOperationName: operation.name,
      importRuns: metadata?.importRun
        ? replaceRun(state.importRuns, metadata.importRun)
        : state.importRuns
    }));
    await get().refreshWorkspaceDetails(selectedWorkspaceName);
    await get().refreshWorkspaces();
    await get().refreshRecentImportQueries();
  },

  startDiscovery: async () => {
    const selectedWorkspaceName = get().selectedWorkspaceName;
    if (!selectedWorkspaceName) return;

    const response = await workspaceClient.createDiscoveryRun({ parent: selectedWorkspaceName });
    const metadata = response.metadata ? anyUnpack(response.metadata, DiscoveryRunMetadataSchema) : undefined;
    set({
      activeOperationName: response.name,
      selectedRunName: metadata?.discoveryRun?.name || ""
    });
    await get().refreshWorkspaceDetails(selectedWorkspaceName);
    await get().refreshWorkspaces();
  }
}));

function errorMessage(error: unknown) {
  if (error instanceof ConnectError) {
    return error.rawMessage;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "Unexpected error";
}

function isPattern(value: Pattern | undefined): value is Pattern {
  return value !== undefined;
}

function replaceRun<T extends { name: string }>(runs: T[], replacement: T) {
  const index = runs.findIndex((run) => run.name === replacement.name);
  if (index < 0) {
    return [replacement, ...runs];
  }
  return runs.map((run, runIndex) => runIndex === index ? replacement : run);
}

function findActiveOperationName(importRuns: ImportRun[], discoveryRuns: DiscoveryRun[]) {
  const activeImport = importRuns.find(
    (run) => run.state === ImportRunState.QUEUED || run.state === ImportRunState.RUNNING
  );
  if (activeImport) {
    return activeImport.name.replace("/importRuns/", "/operations/");
  }

  const activeDiscovery = discoveryRuns.find(
    (run) => run.state === DiscoveryRunState.QUEUED || run.state === DiscoveryRunState.RUNNING
  );
  return activeDiscovery?.name.replace("/discoveryRuns/", "/operations/");
}
