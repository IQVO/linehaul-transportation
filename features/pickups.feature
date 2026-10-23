Feature: Pickup departures at vendor nodes
  An ACTIVE vendor fulfillment center with a declared profile gets a pickup
  departure at every cutoff of the 14-day horizon, toward the destination of
  its default outbound lane. Pickups follow the node through suspension,
  retirement and profile revisions.

  Background:
    Given a vendor fulfillment center "fc-1" with a pickup cutoff at "18:00" on weekdays
    And an active sortation center "sc-1"
    And an open GROUND lane "ln-1" from "fc-1" to "sc-1"

  Scenario: Activating the node generates its pickups
    When the node "fc-1" is activated
    Then the command succeeds
    And the node "fc-1" has pickup departures toward "sc-1"
    And the last command published the event "DepartureScheduled"

  Scenario: A node that is not active has no pickups
    Then the node "fc-1" has no scheduled pickup departures

  Scenario: Activation delivered twice schedules nothing the second time
    When the activation of "fc-1" is delivered twice with the same event id
    Then the departures from "fc-1" were scheduled once

  Scenario: Suspending the node cancels its future pickups
    Given the node "fc-1" is activated
    When the node "fc-1" is suspended
    Then the command succeeds
    And the node "fc-1" has no scheduled pickup departures
    And the last command published the event "DepartureCancelled"
    And every pickup departure of "fc-1" is cancelled because "NODE_SUSPENDED"

  Scenario: Retiring the node cancels its future pickups
    Given the node "fc-1" is activated
    When the node "fc-1" is retired
    Then the node "fc-1" has no scheduled pickup departures
    And every pickup departure of "fc-1" is cancelled because "NODE_RETIRED"

  Scenario: A revised profile that drops the cutoff cancels the pickups it no longer declares
    Given the node "fc-1" is activated
    When the profile of "fc-1" is revised to revision 2 with no cutoff
    Then the node "fc-1" has no scheduled pickup departures
    And every pickup departure of "fc-1" is cancelled because "PROFILE_REVISED"

  Scenario: An older profile revision is ignored
    Given the node "fc-1" is activated
    When the profile of "fc-1" is revised to revision 1 with no cutoff
    Then the node "fc-1" has pickup departures toward "sc-1"
