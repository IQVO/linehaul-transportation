Feature: Loads, dispatch and arrival
  A load collects packages onto a departure. Closing it closes the departure,
  dispatching it records the departure of the trip and receiving it records
  the arrival. LoadDeparted and LoadArrived carry the departure kind and the
  type of the node where they happened.

  Background:
    Given an active sortation center "sc-1"
    And an active delivery station "ds-1"
    And an open GROUND lane "ln-2" from "sc-1" to "ds-1"
    And the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 2 packages
    And departures are generated
    And the line-haul departure "H1" is the 1st on lane "ln-2"

  Scenario: A load goes from OPEN to ARRIVED
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the load "LD1" is closed
    And the clock moves to "2026-10-24T00:30:00Z"
    When I dispatch the load "LD1"
    Then the command succeeds
    And the load "LD1" is DEPARTED
    And the last command published the event "LoadDeparted"
    And the last LoadDeparted of "LD1" carries kind "LINEHAUL" and node type "SORTATION_CENTER"
    When I receive the load "LD1"
    Then the load "LD1" is ARRIVED
    And the last command published the event "LoadArrived"
    And the last LoadArrived of "LD1" carries kind "LINEHAUL" and node type "DELIVERY_STATION"
    And the departure "H1" is ARRIVED

  Scenario: Adding a package to a line-haul load books the departure
    Given I open the load "LD1" on the departure "H1"
    When the package "TRK1" is added to the load "LD1"
    Then the departure "H1" holds 1 booked package

  Scenario: A package cannot ride two active loads
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the line-haul departure "H2" is the 2nd on lane "ln-2"
    And I open the load "LD2" on the departure "H2"
    When the package "TRK1" is added to the load "LD2"
    Then the command is refused with "package-already-loaded"

  Scenario: A departure takes one load
    Given I open the load "LD1" on the departure "H1"
    When I open the load "LD2" on the departure "H1"
    Then the command is refused with "load-already-exists"

  Scenario: A load cannot hold more than the departure capacity
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the package "TRK2" is added to the load "LD1"
    When the package "TRK3" is added to the load "LD1"
    Then the command is refused with "load-over-capacity"

  Scenario: An OPEN load cannot be dispatched
    Given I open the load "LD1" on the departure "H1"
    When I dispatch the load "LD1"
    Then the command is refused with "illegal-transition"

  Scenario: A DEPARTED load cannot take packages
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the load "LD1" is closed
    And the clock moves to "2026-10-24T00:30:00Z"
    And I dispatch the load "LD1"
    When the package "TRK2" is added to the load "LD1"
    Then the command is refused with "illegal-transition"

  Scenario: Dispatching twice is a no-op
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the load "LD1" is closed
    And the clock moves to "2026-10-24T00:30:00Z"
    And I dispatch the load "LD1"
    When I dispatch the load "LD1"
    Then the command succeeds
    And no event was published by the last command

  Scenario: A cancelled departure cannot be loaded
    Given the departure "H1" is cancelled because "breakdown"
    When I open the load "LD1" on the departure "H1"
    Then the command is refused with "illegal-transition"
