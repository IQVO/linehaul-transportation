Feature: Load rules on line-haul, air and pickup departures
  OpenLoad, AddPackage, CloseLoad, DispatchLoad and ReceiveLoad (use cases;
  aggregate canvas Load invariants; ADR 0002 A19-A23): the package uniqueness
  window, the capacity snapshot, when a package books the departure, the state
  machine of load and departure, and the payload of the three load events. The
  clock is Friday 2026-10-23 12:00 UTC; the first departure of lane ln-2 cuts
  off at 2026-10-23T23:00:00Z.

  Background:
    Given an active sortation center "sc-1"
    And an active delivery station "ds-1"
    And an open GROUND lane "ln-2" from "sc-1" to "ds-1"
    And the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 2 packages
    And departures are generated
    And the line-haul departure "H1" is the 1st on lane "ln-2"

  # --- AddPackage: uniqueness and booking --------------------------------------

  Scenario: Adding a tracking number the load already holds changes nothing
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    When the package "TRK1" is added to the load "LD1"
    Then the command succeeds
    And the load "LD1" holds 1 package
    And the departure "H1" holds 1 booked package

  Scenario: A package on a closed load is still taken
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the line-haul departure "H2" is the 2nd on lane "ln-2"
    And I open the load "LD2" on the departure "H2"
    And the load "LD1" is closed
    When the package "TRK1" is added to the load "LD2"
    Then the command is refused with "package-already-loaded"

  Scenario: A package whose load has departed can be loaded again
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the line-haul departure "H2" is the 2nd on lane "ln-2"
    And I open the load "LD2" on the departure "H2"
    And the load "LD1" is closed
    And the clock moves to "2026-10-24T00:30:00Z"
    And I dispatch the load "LD1"
    When the package "TRK1" is added to the load "LD2"
    Then the command succeeds
    And the load "LD2" holds 1 package

  Scenario: A package is booked on a line-haul departure at exactly its cutoff
    Given I open the load "LD1" on the departure "H1"
    And the clock moves to "2026-10-23T23:00:00Z"
    When the package "TRK1" is added to the load "LD1"
    Then the command succeeds
    And the departure "H1" holds 1 booked package

  Scenario: A package one second after the cutoff is refused and not kept
    Given I open the load "LD1" on the departure "H1"
    And the clock moves to "2026-10-23T23:00:01Z"
    When the package "TRK1" is added to the load "LD1"
    Then the command is refused with "departure-closed"
    And the load "LD1" holds 0 packages
    And the departure "H1" holds 0 booked packages

  Scenario: A capacity cut below the load snapshot refuses the next package as departure-full
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the capacity of "H1" is changed to 1
    When the package "TRK2" is added to the load "LD1"
    Then the command is refused with "departure-full"
    And the load "LD1" holds 1 package
    And the departure "H1" holds 1 booked package

  Scenario: An open load keeps the capacity it was opened with
    Given I open the load "LD1" on the departure "H1"
    And the capacity of "H1" is changed to 5
    And the package "TRK1" is added to the load "LD1"
    And the package "TRK2" is added to the load "LD1"
    When the package "TRK3" is added to the load "LD1"
    Then the command is refused with "load-over-capacity"
    And the load "LD1" was opened with capacity 2
    And the departure "H1" holds 2 booked packages

  Scenario Outline: A tracking number of 1 to 64 characters is accepted
    Given I open the load "LD1" on the departure "H1"
    When a tracking number of <length> characters is added to the load "LD1"
    Then the command succeeds
    And the load "LD1" holds 1 package

    Examples:
      | length |
      | 1      |
      | 64     |

  Scenario Outline: A tracking number that is empty or longer than 64 characters is refused
    Given I open the load "LD1" on the departure "H1"
    When a tracking number of <length> characters is added to the load "LD1"
    Then the command is refused with "validation-failed"
    And the load "LD1" holds 0 packages

    Examples:
      | length |
      | 0      |
      | 65     |

  # --- tracking numbers are trimmed text -----------------------------------------

  Scenario: A tracking number with a trailing blank is refused
    Given I open the load "LD1" on the departure "H1"
    When the package "TRK1 " is added to the load "LD1"
    Then the command is refused with "validation-failed"
    And the load "LD1" holds 0 packages

  # --- pickup loads: the lock already booked the package -----------------------

  Scenario: Packages added to a pickup load do not book the pickup again
    Given a vendor fulfillment center "fc-1" with a pickup cutoff at "18:00" on weekdays
    And an open GROUND lane "ln-1" from "fc-1" to "sc-1"
    And the node "fc-1" is activated
    And the pickup departure "P1" is the 1st from "fc-1"
    And the assignment "as-1" is locked on "P1"
    And I open the load "LDP" on the departure "P1"
    When the package "TRK-A" is added to the load "LDP"
    And the package "TRK-B" is added to the load "LDP"
    Then the load "LDP" holds 2 packages
    And the departure "P1" holds 1 booked package

  Scenario: A pickup load announces the vendor node on departure and the sortation center on arrival
    Given a vendor fulfillment center "fc-1" with a pickup cutoff at "18:00" on weekdays
    And an open GROUND lane "ln-1" from "fc-1" to "sc-1"
    And the node "fc-1" is activated
    And the pickup departure "P1" is the 1st from "fc-1"
    And I open the load "LDP" on the departure "P1"
    And the package "TRK-A" is added to the load "LDP"
    And the load "LDP" is closed
    When I dispatch the load "LDP"
    Then the last LoadDeparted of "LDP" carries kind "PICKUP" and node type "FULFILLMENT_CENTER"
    And the last LoadDeparted of "LDP" lists "TRK-A" from node "fc-1"
    When I receive the load "LDP"
    Then the last LoadArrived of "LDP" carries kind "PICKUP" and node type "SORTATION_CENTER"
    And the last LoadArrived of "LDP" lists "TRK-A" at node "sc-1"

  Scenario: An air load announces the kind AIR and the air hub on arrival
    Given an active air hub "ah-1"
    And an open AIR lane "ln-5" from "sc-1" to "ah-1"
    And the schedule of "ln-5" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 2 packages
    And departures are generated
    And the departure "A1" is the one on lane "ln-5" cutting off at "2026-10-23T23:00:00Z"
    And I open the load "LDA" on the departure "A1"
    When the package "TRK-A" is added to the load "LDA"
    Then the departure "A1" holds 1 booked package
    When the load "LDA" is closed
    And I dispatch the load "LDA"
    Then the last LoadDeparted of "LDA" carries kind "AIR" and node type "SORTATION_CENTER"
    When I receive the load "LDA"
    Then the last LoadArrived of "LDA" carries kind "AIR" and node type "AIR_HUB"

  # --- the payload of the load events -------------------------------------------

  Scenario: LoadClosed, LoadDeparted and LoadArrived carry the packages and the nodes
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the package "TRK2" is added to the load "LD1"
    When the load "LD1" is closed
    Then the last LoadClosed of "LD1" counts 2 packages
    When I dispatch the load "LD1"
    Then the last LoadDeparted of "LD1" lists "TRK1,TRK2" from node "sc-1"
    When I receive the load "LD1"
    Then the last LoadArrived of "LD1" lists "TRK1,TRK2" at node "ds-1"

  # --- the state machine of load and departure -----------------------------------

  Scenario: A closed load takes no more packages
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the load "LD1" is closed
    When the package "TRK2" is added to the load "LD1"
    Then the command is refused with "illegal-transition"
    And the load "LD1" holds 1 package

  Scenario: Closing a closed load changes nothing and announces nothing
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the load "LD1" is closed
    When the load "LD1" is closed
    Then the command succeeds
    And no event was published by the last command

  Scenario: A load whose departure was cancelled takes no packages
    Given I open the load "LD1" on the departure "H1"
    And the departure "H1" is cancelled because "breakdown"
    When the package "TRK1" is added to the load "LD1"
    Then the command is refused with "illegal-transition"

  Scenario: A load whose departure was cancelled cannot be closed
    Given I open the load "LD1" on the departure "H1"
    And the departure "H1" is cancelled because "breakdown"
    When the load "LD1" is closed
    Then the command is refused with "illegal-transition"
    And the load "LD1" is OPEN

  Scenario: A load cannot be received before it has departed
    Given I open the load "LD1" on the departure "H1"
    When I receive the load "LD1"
    Then the command is refused with "illegal-transition"
    When the load "LD1" is closed
    And I receive the load "LD1"
    Then the command is refused with "illegal-transition"

  Scenario: A load that has arrived cannot be dispatched again
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the load "LD1" is closed
    And I dispatch the load "LD1"
    And I receive the load "LD1"
    When I dispatch the load "LD1"
    Then the command is refused with "illegal-transition"
    And the load "LD1" is ARRIVED

  Scenario: Receiving a load twice changes nothing and announces nothing
    Given I open the load "LD1" on the departure "H1"
    And the package "TRK1" is added to the load "LD1"
    And the load "LD1" is closed
    And I dispatch the load "LD1"
    And I receive the load "LD1"
    When I receive the load "LD1"
    Then the command succeeds
    And no event was published by the last command

  Scenario: A second load cannot be opened once the departure is closed
    Given I open the load "LD1" on the departure "H1"
    And the load "LD1" is closed
    When I open the load "LD2" on the departure "H1"
    Then the command is refused with "illegal-transition"

  Scenario: A departure that a load closed can no longer be cancelled
    Given I open the load "LD1" on the departure "H1"
    And the load "LD1" is closed
    When the departure "H1" is cancelled because "too late"
    Then the command is refused with "illegal-transition"
    And the departure "H1" is CLOSED

  Scenario: A departure that has left can no longer be cancelled
    Given I open the load "LD1" on the departure "H1"
    And the load "LD1" is closed
    And I dispatch the load "LD1"
    When the departure "H1" is cancelled because "too late"
    Then the command is refused with "illegal-transition"
    And the departure "H1" is DEPARTED

  Scenario Outline: A command on a load that does not exist is not found
    When I <command> the unknown load "ghost"
    Then the command is refused with "not-found"

    Examples:
      | command           |
      | add a package to  |
      | close             |
      | dispatch          |
      | receive           |

  Scenario: A load cannot be opened on a departure that does not exist
    When I open the load "LD1" on the departure "dep-ghost"
    Then the command is refused with "not-found"
