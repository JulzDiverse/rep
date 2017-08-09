package rep

type ScoreFunc func(*CellState, *Resource, float64) float64
type ScoreTypeFunc func(*ScoreType)

type ScoreType struct {
	Compute ScoreFunc
}

func NewScoreType(f ScoreTypeFunc) *ScoreType {
	var st ScoreType
	f(&st)
	return &st
}

func BestFitFashion(st *ScoreType) {
	st.Compute = bestFit
}

func WorstFitFashion(st *ScoreType) {
	st.Compute = computeScore
}

func computeScore(c *CellState, res *Resource, startingContainerWeight float64) float64 {
	remainingResources := c.AvailableResources.Copy()
	remainingResources.Subtract(res)
	startingContainerScore := float64(c.StartingContainerCount) * startingContainerWeight
	return remainingResources.ComputeScore(&c.TotalResources) + startingContainerScore
}

func bestFit(c *CellState, res *Resource, startingContainerWeight float64) float64 {
	total := c.TotalResources
	remainingResources := c.AvailableResources.Copy()
	remainingResources.Subtract(res)

	fractionUsedMemory := float64(remainingResources.MemoryMB) / float64(total.MemoryMB)
	fractionUsedDisk := float64(remainingResources.DiskMB) / float64(total.DiskMB)
	fractionUsedContainers := ((float64(remainingResources.Containers) / float64(total.Containers)) * 3) // give more weigh to cells with a high container load

	return (fractionUsedMemory + fractionUsedDisk + fractionUsedContainers) / 5.0
}
