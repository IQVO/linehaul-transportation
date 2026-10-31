import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/linehaul-transportation-api",
    },
    {
      type: "category",
      label: "Lane schedules",
      link: {
        type: "doc",
        id: "api-reference/rest/lane-schedules",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/define-lane-schedule",
          label: "Define the weekly schedule of a lane (DefineLaneSchedule)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/retire-lane-schedule",
          label: "Retire the schedule of a lane (RetireLaneSchedule)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Departures",
      link: {
        type: "doc",
        id: "api-reference/rest/departures",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-departures",
          label: "List departures by origin, destination and time window (ListDepartures)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-departure",
          label: "Get one departure",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-departure",
          label: "Cancel a scheduled departure (CancelDeparture)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/change-departure-capacity",
          label: "Change the package capacity of a scheduled departure (ChangeDepartureCapacity)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Loads",
      link: {
        type: "doc",
        id: "api-reference/rest/loads",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/open-load",
          label: "Open a load on a departure (OpenLoad)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/add-package-to-load",
          label: "Add a package to an open load (AddPackage)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/close-load",
          label: "Close an open load (CloseLoad)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/dispatch-load",
          label: "Dispatch a closed load (DispatchLoad)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/receive-load",
          label: "Receive a load at its destination node (ReceiveLoad)",
          className: "api-method post",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
